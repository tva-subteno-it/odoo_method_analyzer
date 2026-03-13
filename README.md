# Odoo Method Analyzer

`odoo_method_analyzer` is a small Go CLI that scans an Odoo project and highlights:

- customer methods that appear to be unused
- `super().method_name(...)` calls that do not resolve to a method found in the indexed Odoo sources

The tool is designed for addon cleanup and migration work, where you want a fast signal on dead code and suspicious inheritance chains.

## What It Analyzes

The analyzer treats the current working directory as the project root and then:

1. discovers customer addon paths and Odoo source paths
2. loads Python files from customer addons to extract method definitions
3. indexes Python, JavaScript, and XML files from both customer and Odoo paths
4. searches for method usage with a set of heuristic patterns
5. reports unused methods and orphaned `super()` calls

By default, files whose path contains `test` are excluded. Files larger than 1 MiB are also skipped.

## Path Discovery

Path discovery happens in this order:

1. If `get_project_data -a` is available, its comma-separated addon paths are used.
2. Paths ending with `/customer` are treated as customer source paths.
3. All other discovered addon paths are treated as Odoo paths.
4. If `get_project_data` is unavailable or returns nothing useful, the tool falls back to common local layouts.

Fallback customer paths:

- `./code/modules/customer`
- `./modules/customer`

Fallback Odoo paths:

- `./code/modules/extra`
- `./modules/extra`
- `./addons`
- `./odoo/addons`
- `./enterprise`
- `./themes`
- `../odoo*/odoo/addons`
- `../odoo*/addons`
- `../odoo*/enterprise`
- `../odoo*/themes`

If no customer source path is found, the command exits with an error.

## Requirements

- Go installed locally to build or run the CLI
- an Odoo project layout that the path discovery can resolve
- optionally, `get_project_data` in your `PATH` for more accurate addon discovery

The module currently targets Go `1.25.0` in `go.mod`.

## Build

From the project directory:

```bash
go build -o bin/odoo_method_analyzer ./cmd/odoo_method_analyzer
```

## Usage

Run the analyzer from the root of the Odoo project you want to inspect:

```bash
/path/to/odoo_method_analyzer
```

Or run it directly with Go during development:

```bash
go run ./cmd/odoo_method_analyzer
```

Help output:

```text
Usage: odoo_method_analyzer [OPTIONS]

Analyze Odoo customer method usage across the project.

  -h              show help
  -help           show help
  -t              include test files in analysis
  -include-tests  include test files in analysis
  -v              enable verbose output
  -verbose        enable verbose output
  -o <file>       write detailed JSON output to file
  -output <file>  write detailed JSON output to file
```

Examples:

```bash
# Analyze the current Odoo project
odoo_method_analyzer

# Include test files
odoo_method_analyzer --include-tests

# Print extra progress and debug information
odoo_method_analyzer --verbose

# Save the full result as JSON
odoo_method_analyzer --output method-analysis.json
```

## Console Output

The terminal report includes:

- number of used methods
- number of unused methods
- number of orphaned `super()` calls
- the list of orphaned `super()` calls
- the list of unused methods
- in verbose mode, the list of used methods

Unused methods that contain a local `super()` call are marked internally, and overrides are labeled as `[OVERRIDE]` in the console output.

## JSON Output

When `--output` is provided, the tool writes a formatted JSON report with:

- timestamp
- analyzed root path
- discovered source and Odoo paths
- method totals
- used methods
- unused methods
- orphaned `super()` calls
- all analyzed methods with usage metadata

Each method entry can include:

- class name
- file path and line number
- usage count
- whether it is considered used
- whether it looks like an override
- whether it has an orphaned `super()` call
- the first usage hit found

## Detection Heuristics

Method extraction is limited to Python `def` definitions found in customer source paths.

The analyzer skips:

- dunder methods such as `__init__`
- methods whose name starts with `_default`
- `_table_query`
- methods decorated with `@http.route`

Methods are considered used when the first matching hit is found in indexed Python, JavaScript, or XML files.

Python usage patterns include:

- `record.method_name(...)`
- assignments such as `handler = obj.method_name`
- direct calls such as `method_name(...)`
- field string references such as `compute='_method_name'`
- `@api.depends`, `@api.constrains`, and `@api.onchange` references
- `search='method_name'`
- general string references like `'method_name'`
- `default=method_name`

JavaScript usage patterns include direct calls and string references.

XML usage patterns include references in attributes such as `name`, `context`, `attrs`, `domain`, and general text matches.

The analyzer also treats some dynamic `getattr(...)` patterns as usage when a method name starts with a discovered prefix, including:

- `"prefix%s"`
- `"prefix{}".format(...)`
- Python f-strings

Methods decorated with `@api.onchange`, `@api.constrains`, or `@api.depends` are treated as used even without another explicit hit.

## Orphaned `super()` Calls

The analyzer extracts `super(...).method_name(...)` calls from customer Python files and checks whether that method name exists anywhere in the indexed Odoo Python sources.

If no matching method name is found, the call is reported as orphaned.

This is intentionally simple: it checks method names, not the full inheritance graph.

## Limitations

This tool is heuristic-based. It is useful for triage, but it does not perform full semantic analysis.

Known limitations:

- false positives are possible for methods referenced indirectly or through patterns not covered by the search rules
- false negatives are possible when a string match happens to look like a valid usage
- class inheritance is not resolved precisely
- `super()` validation checks only whether a method name exists in indexed Odoo Python files
- only the first usage hit is stored for each method
- the command always analyzes the current working directory; there is no `--root` flag

Treat the report as a review queue, not as an authoritative deletion list.

## Project Layout

```text
cmd/odoo_method_analyzer/   CLI entrypoint
internal/analyzer/          core scanning and usage detection
internal/project/           project path discovery
internal/report/            console and JSON reporting
internal/model/             shared data structures
internal/ui/                terminal output helpers
bin/                        compiled binary output
```
