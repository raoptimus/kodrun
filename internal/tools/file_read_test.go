/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToInt_ReturnsInt_Successfully(t *testing.T) {
	tests := []struct {
		name string
		v    any
		def  int
		want int
	}{
		{
			name: "float64 value",
			v:    float64(42),
			def:  0,
			want: 42,
		},
		{
			name: "int value",
			v:    10,
			def:  0,
			want: 10,
		},
		{
			name: "int64 value",
			v:    int64(99),
			def:  0,
			want: 99,
		},
		{
			name: "nil returns default",
			v:    nil,
			def:  500,
			want: 500,
		},
		{
			name: "string returns default",
			v:    "42",
			def:  100,
			want: 100,
		},
		{
			name: "bool returns default",
			v:    true,
			def:  7,
			want: 7,
		},
		{
			name: "float64 zero",
			v:    float64(0),
			def:  99,
			want: 0,
		},
		{
			name: "negative float64",
			v:    float64(-5),
			def:  0,
			want: -5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toInt(tt.v, tt.def)

			assert.Equal(t, tt.want, got)
		})
	}
}
