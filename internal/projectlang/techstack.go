package projectlang

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Technology identifies a technology or framework used by a project.
type Technology string

const (
	TechGRPC       Technology = "grpc"
	TechHTTP       Technology = "http"
	TechPostgres   Technology = "postgres"
	TechClickhouse Technology = "clickhouse"
	TechMongo      Technology = "mongodb"
	TechRedis      Technology = "redis"
	TechKafka      Technology = "kafka"
)

// AllTechnologies is the full list of detectable technologies.
var AllTechnologies = []Technology{
	TechGRPC, TechHTTP, TechPostgres, TechClickhouse,
	TechMongo, TechRedis, TechKafka,
}

// TechStack is a set of technologies detected in a project.
type TechStack []Technology

// Has reports whether the stack contains the given technology.
func (ts TechStack) Has(t Technology) bool {
	return slices.Contains(ts, t)
}

// HasAll reports whether the stack contains all given technology identifiers.
func (ts TechStack) HasAll(required []string) bool {
	for _, r := range required {
		if !ts.Has(Technology(r)) {
			return false
		}
	}
	return true
}

// Strings returns the stack as a slice of strings.
func (ts TechStack) Strings() []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}

// String returns a comma-separated list of technologies.
func (ts TechStack) String() string {
	return strings.Join(ts.Strings(), ", ")
}

// goModRule maps a module prefix in go.mod to a Technology.
type goModRule struct {
	prefix string
	tech   Technology
}

// goModRules lists known Go module prefixes and the technologies they indicate.
var goModRules = []goModRule{
	{"google.golang.org/grpc", TechGRPC},
	{"github.com/jackc/pgx", TechPostgres},
	{"github.com/lib/pq", TechPostgres},
	{"github.com/jmoiron/sqlx", TechPostgres},
	{"github.com/ClickHouse/clickhouse-go", TechClickhouse},
	{"go.mongodb.org/mongo-driver", TechMongo},
	{"github.com/redis/go-redis", TechRedis},
	{"github.com/gomodule/redigo", TechRedis},
	{"github.com/segmentio/kafka-go", TechKafka},
	{"github.com/IBM/sarama", TechKafka},
	{"github.com/go-chi/chi", TechHTTP},
	{"github.com/labstack/echo", TechHTTP},
	{"github.com/gin-gonic/gin", TechHTTP},
	{"github.com/gofiber/fiber", TechHTTP},
}

// protoDirs are directories checked for .proto files as a gRPC signal.
var protoDirs = []string{".", "proto", "api"}

// TechDetector inspects a working directory and detects the technology stack.
type TechDetector struct {
	workDir string
}

// NewTechDetector creates a TechDetector for the given working directory.
func NewTechDetector(workDir string) *TechDetector {
	return &TechDetector{workDir: workDir}
}

// Detect returns the technologies used by the project.
// For Go projects it inspects go.mod and checks for .proto files.
func (d *TechDetector) Detect(lang Language) TechStack {
	if d == nil || d.workDir == "" {
		return nil
	}

	switch lang {
	case LangGo:
		return d.detectGo()
	default:
		return nil
	}
}

func (d *TechDetector) detectGo() TechStack {
	seen := make(map[Technology]bool)

	d.scanGoMod(seen)
	d.scanProtoFiles(seen)

	stack := make(TechStack, 0, len(seen))
	for _, t := range AllTechnologies {
		if seen[t] {
			stack = append(stack, t)
		}
	}
	return stack
}

func (d *TechDetector) scanGoMod(seen map[Technology]bool) {
	f, err := os.Open(filepath.Join(d.workDir, "go.mod"))
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		for _, rule := range goModRules {
			if strings.HasPrefix(line, rule.prefix) || strings.Contains(line, rule.prefix) {
				seen[rule.tech] = true
			}
		}
	}
}

func (d *TechDetector) scanProtoFiles(seen map[Technology]bool) {
	if seen[TechGRPC] {
		return
	}
	for _, dir := range protoDirs {
		pattern := filepath.Join(d.workDir, dir, "*.proto")
		matches, err := filepath.Glob(pattern)
		if err == nil && len(matches) > 0 {
			seen[TechGRPC] = true
			return
		}
	}
}
