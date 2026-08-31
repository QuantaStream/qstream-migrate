package loadplan

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/QuantaStream/qstream-migrate/internal/model"
)

type Options struct {
	OutDir           string
	RelationshipMode string
	LoaderTarget     string
	BatchSize        int
	Overwrite        bool
}

type Result struct {
	Files     []string
	LoadOrder []string
}

func Write(plan model.MigrationPlan, opts Options) (Result, error) {
	opts = normalizeOptions(opts)
	order, err := LoadOrder(plan, opts.RelationshipMode)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create output directory: %w", err)
	}

	tables := includedTablesByName(plan)
	var result Result
	result.LoadOrder = append(result.LoadOrder, order...)
	if err := writeText(&result, filepath.Join(opts.OutDir, "load-order.txt"), strings.Join(order, "\n")+"\n", 0o644, opts.Overwrite); err != nil {
		return Result{}, err
	}
	if err := writeText(&result, filepath.Join(opts.OutDir, "README.md"), renderReadme(plan, opts, order), 0o644, opts.Overwrite); err != nil {
		return Result{}, err
	}
	for _, tableName := range order {
		table := tables[tableName]
		sql, err := exportSQL(plan, table)
		if err != nil {
			return Result{}, err
		}
		if err := writeText(&result, filepath.Join(opts.OutDir, "queries", tableName+".json.sql"), sql, 0o644, opts.Overwrite); err != nil {
			return Result{}, err
		}
	}
	if err := writeText(&result, filepath.Join(opts.OutDir, "scripts", "export-jsonl.sh"), exportScript(), 0o755, opts.Overwrite); err != nil {
		return Result{}, err
	}
	if err := writeText(&result, filepath.Join(opts.OutDir, "scripts", "post-jsonl.sh"), postScript(opts), 0o755, opts.Overwrite); err != nil {
		return Result{}, err
	}
	return result, nil
}

func normalizeOptions(opts Options) Options {
	if opts.OutDir == "" {
		opts.OutDir = "migration-load"
	}
	if opts.RelationshipMode == "" {
		opts.RelationshipMode = "metadata"
	}
	if opts.LoaderTarget == "" {
		opts.LoaderTarget = "http://127.0.0.1:8088/ingest/json"
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 2000
	}
	return opts
}

func LoadOrder(plan model.MigrationPlan, relationshipMode string) ([]string, error) {
	tables := includedTablesByName(plan)
	if len(tables) == 0 {
		return nil, fmt.Errorf("plan has no included tables")
	}

	deps := make(map[string]map[string]struct{}, len(tables))
	children := make(map[string][]string, len(tables))
	for tableName := range tables {
		deps[tableName] = map[string]struct{}{}
	}
	for _, table := range plan.Tables {
		if !table.Include {
			continue
		}
		for _, relationship := range table.Relationships {
			if !includeRelationship(relationship, relationshipMode) {
				continue
			}
			if _, ok := tables[relationship.ParentTable]; !ok {
				return nil, fmt.Errorf("table %s relationship %s parent table %s is not included", table.Name, relationship.Name, relationship.ParentTable)
			}
			if relationship.ParentTable == table.Name {
				continue
			}
			deps[table.Name][relationship.ParentTable] = struct{}{}
			children[relationship.ParentTable] = append(children[relationship.ParentTable], table.Name)
		}
	}

	ready := make([]string, 0, len(tables))
	for tableName, tableDeps := range deps {
		if len(tableDeps) == 0 {
			ready = append(ready, tableName)
		}
	}
	sort.Strings(ready)

	var order []string
	for len(ready) > 0 {
		tableName := ready[0]
		ready = ready[1:]
		order = append(order, tableName)
		sort.Strings(children[tableName])
		for _, child := range children[tableName] {
			delete(deps[child], tableName)
			if len(deps[child]) == 0 {
				ready = append(ready, child)
				sort.Strings(ready)
			}
		}
		delete(deps, tableName)
	}
	if len(deps) > 0 {
		cycleTables := make([]string, 0, len(deps))
		for tableName := range deps {
			cycleTables = append(cycleTables, tableName)
		}
		sort.Strings(cycleTables)
		return nil, fmt.Errorf("relationship cycle prevents load ordering: %s", strings.Join(cycleTables, ", "))
	}
	return order, nil
}

func includedTablesByName(plan model.MigrationPlan) map[string]model.TablePlan {
	tables := make(map[string]model.TablePlan)
	for _, table := range plan.Tables {
		if table.Include {
			tables[table.Name] = table
		}
	}
	return tables
}

func includeRelationship(relationship model.RelationshipPlan, mode string) bool {
	switch mode {
	case "all":
		return relationship.Kind == "foreign_key" || relationship.Kind == "candidate_foreign_key"
	case "metadata":
		return relationship.Kind == "foreign_key"
	default:
		return false
	}
}

func exportSQL(plan model.MigrationPlan, table model.TablePlan) (string, error) {
	fields := includedFields(table)
	if len(fields) == 0 {
		return "", fmt.Errorf("table %s has no included fields", table.Name)
	}
	sourceTable := table.SourceName
	if sourceTable == "" {
		sourceTable = table.Name
	}

	var b strings.Builder
	b.WriteString("SELECT JSON_OBJECT(\n")
	b.WriteString("  'type', ")
	b.WriteString(sqlString(table.Name))
	b.WriteString(",\n")
	b.WriteString("  'data', JSON_OBJECT(\n")
	for i, field := range fields {
		if i > 0 {
			b.WriteString(",\n")
		}
		sourceName := field.SourceName
		if sourceName == "" {
			sourceName = field.Name
		}
		b.WriteString("    ")
		b.WriteString(sqlString(field.Name))
		b.WriteString(", ")
		b.WriteString(quoteIdent(sourceName))
	}
	b.WriteString("\n  )\n")
	b.WriteString(")\n")
	b.WriteString("FROM ")
	b.WriteString(qualifiedSourceTable(plan.Source.Schema, sourceTable))
	if len(table.PrimaryKey) > 0 {
		b.WriteString("\nORDER BY ")
		for i, field := range table.PrimaryKey {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(quoteIdent(field))
		}
	}
	b.WriteString(";\n")
	return b.String(), nil
}

func includedFields(table model.TablePlan) []model.FieldPlan {
	var fields []model.FieldPlan
	for _, field := range table.Fields {
		if field.Include {
			fields = append(fields, field)
		}
	}
	sort.SliceStable(fields, func(i, j int) bool {
		return fields[i].SourceOrdinal < fields[j].SourceOrdinal
	})
	return fields
}

func qualifiedSourceTable(schema, table string) string {
	if strings.Contains(table, ".") {
		parts := strings.Split(table, ".")
		quoted := make([]string, 0, len(parts))
		for _, part := range parts {
			quoted = append(quoted, quoteIdent(part))
		}
		return strings.Join(quoted, ".")
	}
	if strings.TrimSpace(schema) == "" {
		return quoteIdent(table)
	}
	return quoteIdent(schema) + "." + quoteIdent(table)
}

func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func sqlString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func writeText(result *Result, path, body string, perm os.FileMode, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("file already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(body), perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	result.Files = append(result.Files, path)
	return nil
}

func renderReadme(plan model.MigrationPlan, opts Options, order []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# qstream-migrate Load Plan\n\n")
	fmt.Fprintf(&b, "Source: `%s` schema `%s`\n\n", plan.Source.Kind, plan.Source.Schema)
	fmt.Fprintf(&b, "Loader target: `%s`\n\n", opts.LoaderTarget)
	fmt.Fprintf(&b, "Batch size: `%d`\n\n", opts.BatchSize)
	fmt.Fprintf(&b, "Relationship mode: `%s`\n\n", opts.RelationshipMode)
	fmt.Fprintf(&b, "## Files\n\n")
	fmt.Fprintf(&b, "- `load-order.txt`: parent-before-child table load order.\n")
	fmt.Fprintf(&b, "- `queries/*.json.sql`: MySQL export queries that produce one QuantaStream JSON event per row.\n")
	fmt.Fprintf(&b, "- `scripts/export-jsonl.sh`: exports each table to `exports/<table>.jsonl`.\n")
	fmt.Fprintf(&b, "- `scripts/post-jsonl.sh`: posts exported JSONL files to the QuantaStream loader in batches.\n\n")
	fmt.Fprintf(&b, "## Load Order\n\n")
	for i, tableName := range order {
		fmt.Fprintf(&b, "%d. `%s`\n", i+1, tableName)
	}
	fmt.Fprintf(&b, "\n## Usage\n\n")
	fmt.Fprintf(&b, "Review the generated SQL before running it. Then export from MySQL:\n\n")
	fmt.Fprintf(&b, "```bash\n")
	fmt.Fprintf(&b, "export MYSQL_HOST=127.0.0.1\n")
	fmt.Fprintf(&b, "export MYSQL_PORT=3306\n")
	fmt.Fprintf(&b, "export MYSQL_USER=myuser\n")
	fmt.Fprintf(&b, "export MYSQL_DATABASE=%s\n", shellSingleQuote(plan.Source.Schema))
	fmt.Fprintf(&b, "# optional: export MYSQL_PWD='your-password'\n")
	fmt.Fprintf(&b, "./scripts/export-jsonl.sh\n")
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "Post the exported rows to the QuantaStream loader:\n\n")
	fmt.Fprintf(&b, "```bash\n")
	fmt.Fprintf(&b, "export QS_LOADER_TARGET=%s\n", shellSingleQuote(opts.LoaderTarget))
	fmt.Fprintf(&b, "export BATCH_SIZE=%d\n", opts.BatchSize)
	fmt.Fprintf(&b, "./scripts/post-jsonl.sh\n")
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "The posting script sends a final commit request when `QS_LOADER_COMMIT_URL` is set. If omitted, it derives `http://host:port/commit` from `QS_LOADER_TARGET` when the target ends in `/ingest/json`.\n")
	return b.String()
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func exportScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXPORT_DIR="${1:-"$ROOT_DIR/exports"}"

: "${MYSQL_USER:?Set MYSQL_USER}"
: "${MYSQL_DATABASE:?Set MYSQL_DATABASE}"
MYSQL_HOST="${MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${MYSQL_PORT:-3306}"

mkdir -p "$EXPORT_DIR"

while IFS= read -r table; do
  [ -z "$table" ] && continue
  echo "exporting $table"
  mysql --batch --raw --skip-column-names \
    -h "$MYSQL_HOST" \
    -P "$MYSQL_PORT" \
    -u "$MYSQL_USER" \
    "$MYSQL_DATABASE" \
    < "$ROOT_DIR/queries/${table}.json.sql" \
    > "$EXPORT_DIR/${table}.jsonl"
done < "$ROOT_DIR/load-order.txt"
`
}

func postScript(opts Options) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXPORT_DIR="${1:-"$ROOT_DIR/exports"}"
QS_LOADER_TARGET="${QS_LOADER_TARGET:-%s}"
BATCH_SIZE="${BATCH_SIZE:-%d}"
QS_LOADER_CHECK_TABLES="${QS_LOADER_CHECK_TABLES:-1}"

if [ -z "${QS_LOADER_COMMIT_URL:-}" ]; then
  if [[ "$QS_LOADER_TARGET" == */ingest/json ]]; then
    QS_LOADER_COMMIT_URL="${QS_LOADER_TARGET%%/ingest/json}/commit"
  fi
fi

if [ -z "${QS_LOADER_STATS_URL:-}" ]; then
  if [[ "$QS_LOADER_TARGET" == */ingest/json ]]; then
    QS_LOADER_STATS_URL="${QS_LOADER_TARGET%%/ingest/json}/stats"
  fi
fi

if [ "$QS_LOADER_CHECK_TABLES" != "0" ] && [ -n "${QS_LOADER_STATS_URL:-}" ]; then
  python3 - "$QS_LOADER_STATS_URL" "$ROOT_DIR/load-order.txt" <<'PY'
import json
import sys
import urllib.error
import urllib.request

stats_url, load_order_path = sys.argv[1:3]
with open(load_order_path, "r", encoding="utf-8") as handle:
    expected = [line.strip() for line in handle if line.strip()]

try:
    with urllib.request.urlopen(stats_url, timeout=30) as response:
        body = response.read()
        status_code = response.getcode()
except urllib.error.HTTPError as err:
    detail = err.read().decode("utf-8", "replace")
    raise SystemExit(f"loader table guard failed: HTTP {err.code} from {stats_url}: {detail}")
except Exception as err:
    raise SystemExit(f"loader table guard failed: read {stats_url}: {err}")

if status_code < 200 or status_code >= 300:
    raise SystemExit(f"loader table guard failed: HTTP {status_code} from {stats_url}")

try:
    stats = json.loads(body)
except json.JSONDecodeError as err:
    raise SystemExit(f"loader table guard failed: decode {stats_url}: {err}")

status = stats.get("status")
if status not in (None, "", "ok"):
    raise SystemExit(f"loader table guard failed: loader status is {status!r}")

tables = stats.get("tables")
if not isinstance(tables, list) or not tables:
    raise SystemExit(f"loader table guard failed: {stats_url} did not report mounted table names")

mounted = set(tables)
missing = [table for table in expected if table not in mounted]
if missing:
    raise SystemExit(
        "loader table guard failed: missing expected table(s): "
        + ", ".join(missing)
        + "; mounted table(s): "
        + ", ".join(tables)
    )

print(f"loader table guard ok tables={len(tables)}")
PY
fi

while IFS= read -r table; do
  [ -z "$table" ] && continue
  file="$EXPORT_DIR/${table}.jsonl"
  if [ ! -f "$file" ]; then
    echo "missing export $file" >&2
    exit 1
  fi
  echo "posting $table"
  python3 - "$file" "$QS_LOADER_TARGET" "$BATCH_SIZE" <<'PY'
import json
import sys
import urllib.error
import urllib.request

path, target, batch_size_text = sys.argv[1:4]
batch_size = int(batch_size_text)

def post(records):
    if not records:
        return
    body = json.dumps({"records": records}, separators=(",", ":")).encode("utf-8")
    req = urllib.request.Request(target, data=body, headers={"Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(req) as response:
            response.read()
    except urllib.error.HTTPError as err:
        detail = err.read().decode("utf-8", "replace")
        raise SystemExit(f"loader rejected batch: HTTP {err.code}: {detail}")

sent = 0
records = []
with open(path, "r", encoding="utf-8") as handle:
    for line in handle:
        line = line.strip()
        if not line:
            continue
        records.append(json.loads(line))
        if len(records) >= batch_size:
            post(records)
            sent += len(records)
            records = []
    post(records)
    sent += len(records)
print(f"sent={sent}")
PY
done < "$ROOT_DIR/load-order.txt"

if [ -n "${QS_LOADER_COMMIT_URL:-}" ]; then
  echo "committing loader"
  curl -fsS -X POST "$QS_LOADER_COMMIT_URL" >/dev/null
  echo
fi
`, opts.LoaderTarget, opts.BatchSize)
}
