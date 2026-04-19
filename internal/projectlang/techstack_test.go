/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package projectlang

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTechStack_Has_ContainsTechnology_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stack TechStack
		tech  Technology
		want  bool
	}{
		{
			name:  "single element present",
			stack: TechStack{TechGRPC},
			tech:  TechGRPC,
			want:  true,
		},
		{
			name:  "element present among many",
			stack: TechStack{TechGRPC, TechPostgres, TechRedis},
			tech:  TechPostgres,
			want:  true,
		},
		{
			name:  "element not present",
			stack: TechStack{TechGRPC, TechPostgres},
			tech:  TechRedis,
			want:  false,
		},
		{
			name:  "empty stack returns false",
			stack: TechStack{},
			tech:  TechGRPC,
			want:  false,
		},
		{
			name:  "nil stack returns false",
			stack: nil,
			tech:  TechKafka,
			want:  false,
		},
		{
			name:  "last element present",
			stack: TechStack{TechHTTP, TechMongo, TechKafka},
			tech:  TechKafka,
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.stack.Has(tt.tech)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTechStack_HasAll_AllPresent_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		stack    TechStack
		required []string
		want     bool
	}{
		{
			name:     "all required present",
			stack:    TechStack{TechGRPC, TechPostgres, TechRedis},
			required: []string{"grpc", "postgres"},
			want:     true,
		},
		{
			name:     "single required present",
			stack:    TechStack{TechGRPC},
			required: []string{"grpc"},
			want:     true,
		},
		{
			name:     "empty required always true",
			stack:    TechStack{TechGRPC},
			required: []string{},
			want:     true,
		},
		{
			name:     "nil required always true",
			stack:    TechStack{TechGRPC},
			required: nil,
			want:     true,
		},
		{
			name:     "empty required on empty stack",
			stack:    TechStack{},
			required: []string{},
			want:     true,
		},
		{
			name:     "one missing among required",
			stack:    TechStack{TechGRPC, TechPostgres},
			required: []string{"grpc", "redis"},
			want:     false,
		},
		{
			name:     "all missing",
			stack:    TechStack{TechHTTP},
			required: []string{"grpc", "postgres"},
			want:     false,
		},
		{
			name:     "empty stack with requirements",
			stack:    TechStack{},
			required: []string{"grpc"},
			want:     false,
		},
		{
			name:     "nil stack with requirements",
			stack:    nil,
			required: []string{"grpc"},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.stack.HasAll(tt.required)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTechStack_Strings_ReturnsSlice_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stack TechStack
		want  []string
	}{
		{
			name:  "multiple technologies",
			stack: TechStack{TechGRPC, TechPostgres, TechRedis},
			want:  []string{"grpc", "postgres", "redis"},
		},
		{
			name:  "single technology",
			stack: TechStack{TechKafka},
			want:  []string{"kafka"},
		},
		{
			name:  "empty stack",
			stack: TechStack{},
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.stack.Strings()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTechStack_String_ReturnsCommaSeparated_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stack TechStack
		want  string
	}{
		{
			name:  "multiple technologies",
			stack: TechStack{TechGRPC, TechPostgres, TechRedis},
			want:  "grpc, postgres, redis",
		},
		{
			name:  "single technology",
			stack: TechStack{TechHTTP},
			want:  "http",
		},
		{
			name:  "empty stack",
			stack: TechStack{},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.stack.String()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTechDetector_Detect_NilAndEmpty_Successfully(t *testing.T) {
	t.Parallel()

	t.Run("nil detector returns nil", func(t *testing.T) {
		t.Parallel()
		var d *TechDetector
		got := d.Detect(LangGo)
		assert.Nil(t, got)
	})

	t.Run("empty workDir returns nil", func(t *testing.T) {
		t.Parallel()
		d := NewTechDetector("")
		got := d.Detect(LangGo)
		assert.Nil(t, got)
	})
}

func TestTechDetector_Detect_NonGoLanguage_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lang Language
	}{
		{
			name: "python returns nil",
			lang: LangPython,
		},
		{
			name: "jsts returns nil",
			lang: LangJSTS,
		},
		{
			name: "unknown returns nil",
			lang: LangUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeFile(t, dir, "go.mod", "module x\n\nrequire github.com/jackc/pgx/v5 v5.0.0\n")
			d := NewTechDetector(dir)
			got := d.Detect(tt.lang)
			assert.Nil(t, got)
		})
	}
}

func TestTechDetector_Detect_EmptyGoMod_Successfully(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module x\n\ngo 1.21\n")

	d := NewTechDetector(dir)
	got := d.Detect(LangGo)

	require.NotNil(t, got)
	assert.Empty(t, got)
}

func TestTechDetector_Detect_NoGoMod_Successfully(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	d := NewTechDetector(dir)
	got := d.Detect(LangGo)

	require.NotNil(t, got)
	assert.Empty(t, got)
}

func TestTechDetector_Detect_GoModWithDependencies_Successfully(t *testing.T) {
	t.Parallel()

	type setup func(t *testing.T, dir string)

	tests := []struct {
		name  string
		setup setup
		want  []Technology
	}{
		{
			name: "grpc dependency detected",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire google.golang.org/grpc v1.60.0\n")
			},
			want: []Technology{TechGRPC},
		},
		{
			name: "pgx dependency detected as postgres",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire github.com/jackc/pgx/v5 v5.0.0\n")
			},
			want: []Technology{TechPostgres},
		},
		{
			name: "lib/pq detected as postgres",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire github.com/lib/pq v1.10.0\n")
			},
			want: []Technology{TechPostgres},
		},
		{
			name: "sqlx detected as postgres",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire github.com/jmoiron/sqlx v1.3.5\n")
			},
			want: []Technology{TechPostgres},
		},
		{
			name: "clickhouse-go detected",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire github.com/ClickHouse/clickhouse-go/v2 v2.0.0\n")
			},
			want: []Technology{TechClickhouse},
		},
		{
			name: "mongo-driver detected",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire go.mongodb.org/mongo-driver v1.12.0\n")
			},
			want: []Technology{TechMongo},
		},
		{
			name: "go-redis detected",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire github.com/redis/go-redis/v9 v9.0.0\n")
			},
			want: []Technology{TechRedis},
		},
		{
			name: "redigo detected as redis",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire github.com/gomodule/redigo v1.8.9\n")
			},
			want: []Technology{TechRedis},
		},
		{
			name: "kafka-go detected",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire github.com/segmentio/kafka-go v0.4.0\n")
			},
			want: []Technology{TechKafka},
		},
		{
			name: "sarama detected as kafka",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire github.com/IBM/sarama v1.41.0\n")
			},
			want: []Technology{TechKafka},
		},
		{
			name: "chi detected as http",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire github.com/go-chi/chi/v5 v5.0.0\n")
			},
			want: []Technology{TechHTTP},
		},
		{
			name: "echo detected as http",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire github.com/labstack/echo/v4 v4.0.0\n")
			},
			want: []Technology{TechHTTP},
		},
		{
			name: "gin detected as http",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire github.com/gin-gonic/gin v1.9.0\n")
			},
			want: []Technology{TechHTTP},
		},
		{
			name: "fiber detected as http",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire github.com/gofiber/fiber/v2 v2.50.0\n")
			},
			want: []Technology{TechHTTP},
		},
		{
			name: "multiple dependencies detected",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire (\n"+
						"\tgoogle.golang.org/grpc v1.60.0\n"+
						"\tgithub.com/jackc/pgx/v5 v5.0.0\n"+
						"\tgithub.com/redis/go-redis/v9 v9.0.0\n"+
						"\tgithub.com/segmentio/kafka-go v0.4.0\n"+
						")\n")
			},
			want: []Technology{TechGRPC, TechPostgres, TechRedis, TechKafka},
		},
		{
			name: "duplicate postgres deps produce single entry",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire (\n"+
						"\tgithub.com/jackc/pgx/v5 v5.0.0\n"+
						"\tgithub.com/lib/pq v1.10.0\n"+
						")\n")
			},
			want: []Technology{TechPostgres},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tt.setup(t, dir)
			d := NewTechDetector(dir)
			got := d.Detect(LangGo)

			require.NotNil(t, got)
			assert.Equal(t, TechStack(tt.want), got)
		})
	}
}

func TestTechDetector_Detect_ProtoFilesDetectGRPC_Successfully(t *testing.T) {
	t.Parallel()

	type setup func(t *testing.T, dir string)

	tests := []struct {
		name  string
		setup setup
		want  []Technology
	}{
		{
			name: "proto file in root directory",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod", "module x\n")
				writeFile(t, dir, "service.proto", "syntax = \"proto3\";\n")
			},
			want: []Technology{TechGRPC},
		},
		{
			name: "proto file in proto subdirectory",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod", "module x\n")
				require.NoError(t, os.Mkdir(filepath.Join(dir, "proto"), 0o755))
				writeFile(t, dir, "proto/service.proto", "syntax = \"proto3\";\n")
			},
			want: []Technology{TechGRPC},
		},
		{
			name: "proto file in api subdirectory",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod", "module x\n")
				require.NoError(t, os.Mkdir(filepath.Join(dir, "api"), 0o755))
				writeFile(t, dir, "api/service.proto", "syntax = \"proto3\";\n")
			},
			want: []Technology{TechGRPC},
		},
		{
			name: "grpc already in go.mod skips proto scan",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod",
					"module x\n\nrequire google.golang.org/grpc v1.60.0\n")
				writeFile(t, dir, "service.proto", "syntax = \"proto3\";\n")
			},
			want: []Technology{TechGRPC},
		},
		{
			name: "no proto files no grpc dep",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "go.mod", "module x\n")
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tt.setup(t, dir)
			d := NewTechDetector(dir)
			got := d.Detect(LangGo)

			require.NotNil(t, got)
			if tt.want == nil {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, TechStack(tt.want), got)
			}
		})
	}
}

func TestTechDetector_Detect_OrderMatchesAllTechnologies_Successfully(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "go.mod",
		"module x\n\nrequire (\n"+
			"\tgoogle.golang.org/grpc v1.60.0\n"+
			"\tgithub.com/go-chi/chi/v5 v5.0.0\n"+
			"\tgithub.com/jackc/pgx/v5 v5.0.0\n"+
			"\tgithub.com/ClickHouse/clickhouse-go/v2 v2.0.0\n"+
			"\tgo.mongodb.org/mongo-driver v1.12.0\n"+
			"\tgithub.com/redis/go-redis/v9 v9.0.0\n"+
			"\tgithub.com/segmentio/kafka-go v0.4.0\n"+
			")\n")

	d := NewTechDetector(dir)
	got := d.Detect(LangGo)

	// The result order must follow AllTechnologies order.
	expected := TechStack{
		TechGRPC, TechHTTP, TechPostgres, TechClickhouse,
		TechMongo, TechRedis, TechKafka,
	}
	require.NotNil(t, got)
	assert.Equal(t, expected, got)
}
