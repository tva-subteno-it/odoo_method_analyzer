package model

import "time"

type Config struct {
	Root         string
	IncludeTests bool
	Verbose      bool
	OutputFile   string
}

type ProjectPaths struct {
	SourcePaths []string `json:"source_paths"`
	OdooPaths   []string `json:"odoo_paths"`
}

type MethodDef struct {
	Name       string `json:"name"`
	ClassName  string `json:"class_name"`
	FilePath   string `json:"file_path"`
	LineNumber int    `json:"line_number"`
	IsOverride bool   `json:"is_override"`
}

type UsageHit struct {
	Language string `json:"language"`
	Reason   string `json:"reason"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
}

type SuperCall struct {
	MethodName string `json:"method_name"`
	ClassName  string `json:"class_name"`
	FilePath   string `json:"file_path"`
	LineNumber int    `json:"line_number"`
}

type MethodResult struct {
	Name             string     `json:"name"`
	ClassName        string     `json:"class_name"`
	FilePath         string     `json:"file_path"`
	LineNumber       int        `json:"line_number"`
	UsageCount       int        `json:"usage_count"`
	IsUsed           bool       `json:"is_used"`
	IsOverride       bool       `json:"is_override"`
	HasOrphanedSuper bool       `json:"has_orphaned_super"`
	UsageHits        []UsageHit `json:"usage_hits,omitempty"`
}

type Result struct {
	Timestamp          time.Time      `json:"timestamp"`
	Root               string         `json:"root"`
	IncludeTests       bool           `json:"include_tests"`
	SourcePaths        []string       `json:"source_paths"`
	OdooPaths          []string       `json:"odoo_paths"`
	TotalMethods       int            `json:"total_methods"`
	UsedMethods        []MethodResult `json:"used_methods"`
	UnusedMethods      []MethodResult `json:"unused_methods"`
	OrphanedSuperCalls []SuperCall    `json:"orphaned_super_calls"`
	Methods            []MethodResult `json:"methods"`
}
