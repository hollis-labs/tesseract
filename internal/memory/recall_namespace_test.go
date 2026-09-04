package memory

import (
	"reflect"
	"testing"
)

func TestBuildNamespaceClause(t *testing.T) {
	cases := []struct {
		name     string
		input    []string
		wantSQL  string
		wantArgs []interface{}
	}{
		{
			name:     "empty rejects everything",
			input:    nil,
			wantSQL:  "1=0",
			wantArgs: nil,
		},
		{
			name:     "single exact 4-seg",
			input:    []string{"user/x/memory/notes"},
			wantSQL:  "(r.namespace = ?)",
			wantArgs: []interface{}{"user/x/memory/notes"},
		},
		{
			name:     "single legacy-flat treated as prefix",
			input:    []string{"user/x/memory"},
			wantSQL:  "(r.namespace LIKE ?)",
			wantArgs: []interface{}{"user/x/memory/%"},
		},
		{
			name:     "session legacy-flat treated as prefix",
			input:    []string{"user/x/session/s1/memory"},
			wantSQL:  "(r.namespace LIKE ?)",
			wantArgs: []interface{}{"user/x/session/s1/memory/%"},
		},
		{
			name:     "explicit wildcard treated as prefix",
			input:    []string{"user/x/memory/*"},
			wantSQL:  "(r.namespace LIKE ?)",
			wantArgs: []interface{}{"user/x/memory/%"},
		},
		{
			name:     "mixed exact + prefix",
			input:    []string{"user/x/memory/notes", "user/y/memory"},
			wantSQL:  "(r.namespace = ? OR r.namespace LIKE ?)",
			wantArgs: []interface{}{"user/x/memory/notes", "user/y/memory/%"},
		},
		{
			name:     "knowledge namespace stays exact",
			input:    []string{"user/x/knowledge/portfolio"},
			wantSQL:  "(r.namespace = ?)",
			wantArgs: []interface{}{"user/x/knowledge/portfolio"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sql, args := buildNamespaceClause(tc.input)
			if sql != tc.wantSQL {
				t.Errorf("sql = %q, want %q", sql, tc.wantSQL)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args = %v, want %v", args, tc.wantArgs)
			}
		})
	}
}

func TestMemoryPrefix(t *testing.T) {
	cases := []struct {
		input  string
		want   string
		wantOk bool
	}{
		{"user/x/memory", "user/x/memory", true},
		{"user/x/memory/*", "user/x/memory", true},
		{"user/x/project/p/memory", "user/x/project/p/memory", true},
		{"user/x/session/s/memory", "user/x/session/s/memory", true},
		{"user/x/memory/notes", "", false},
		{"user/x/knowledge/something", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, ok := memoryPrefix(tc.input)
			if ok != tc.wantOk || got != tc.want {
				t.Errorf("memoryPrefix(%q) = (%q, %v), want (%q, %v)",
					tc.input, got, ok, tc.want, tc.wantOk)
			}
		})
	}
}
