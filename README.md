# qstream-migrate

`qstream-migrate` is a migration assistant for bringing existing relational
data into QuantaStream.

The first target is MySQL. The goal is not to clone every MySQL feature or
pretend QuantaStream is an OLTP replacement. The goal is to help a user inspect
an existing MySQL database, understand which tables and fields are good
analytical candidates, generate QuantaStream schema files, move data through the
QuantaStream loader path, and validate the result.

## Purpose

QuantaStream uses schema files for table creation and stores fields using
bitmap-native mapping strategies. That gives the engine much of its analytical
shape, but it also means ordinary MySQL DDL is not enough to produce a good
QuantaStream model.

This project exists to bridge that gap.

A useful migration assistant should:

- inspect an existing MySQL schema;
- profile the source data;
- recommend QuantaStream field mappings;
- generate QuantaStream schema YAML;
- generate load commands or scripts;
- validate migrated row counts and basic aggregate checks;
- call out date/time tables that may need QuantaStream time partitioning;
- leave room for a human to adjust the model before loading serious data.

## Current CLI

The first implemented flow is `analyze mysql`, `check`, `generate`, and
`load-plan`.

`analyze mysql` inspects MySQL metadata, profiles the source data, and writes an
editable migration plan:

```bash
go run ./cmd/qstream-migrate analyze mysql \
  --dsn 'user:pass@tcp(127.0.0.1:3306)/dbname' \
  --out ./migration-plan
```

The output directory contains:

- `inventory.json`: raw MySQL metadata plus collected column profiles;
- `plan.yaml`: editable QuantaStream migration recommendations;
- `README.md`: a generated summary and next steps.

Check the reviewed plan before generating schema files:

```bash
go run ./cmd/qstream-migrate check \
  --plan ./migration-plan/plan.yaml
```

`check` reports unresolved mappings, missing primary keys, relationship-mode
warnings, and time-partitioning candidates. Warnings do not fail the command by
default. Use `--strict` when you want warnings to produce a non-zero exit code.

Useful flags:

```bash
go run ./cmd/qstream-migrate analyze mysql \
  --dsn 'user:pass@tcp(127.0.0.1:3306)/dbname' \
  --tables orders,customers,lineitem \
  --sample-limit 10 \
  --query-timeout 30s \
  --enum-max-distinct 500 \
  --lex-prefix-length 16 \
  --out ./migration-plan
```

To build a local binary:

```bash
go build -o bin/qstream-migrate ./cmd/qstream-migrate
```

`generate` turns the reviewed plan into QuantaStream schema YAML:

```bash
go run ./cmd/qstream-migrate generate \
  --plan ./migration-plan/plan.yaml \
  --out ./configuration
```

Generated schemas are written as:

```text
configuration/<table>/schema.yaml
```

Relationship generation is intentionally conservative. By default,
`--relationship-mode metadata` only emits `ParentRelation` fields for
relationships backed by declared MySQL foreign-key metadata. If the analyzer
found name-based relationship candidates and you have reviewed them, opt in:

```bash
go run ./cmd/qstream-migrate generate \
  --plan ./migration-plan/plan.yaml \
  --out ./configuration \
  --relationship-mode all
```

Use `--relationship-mode none` to generate flat table schemas.

When you have a curated QuantaStream configuration to compare against, use
`compare-schema` to make the differences explicit:

```bash
go run ./cmd/qstream-migrate compare-schema \
  --generated ./configuration \
  --reference /path/to/reference/configuration
```

Differences are reported as review items. They do not fail the command unless
`--strict` is supplied.

Tables with date or timestamp columns are called out in the plan with a
`time_quantum` review block. The analyzer lists candidate fields and a suggested
`YMD` quantum, but it does not choose a partitioning field automatically because
the right choice depends on query and load patterns. Once reviewed, set
`time_quantum.field` in `plan.yaml`; `generate` will then emit
`timeQuantumType` and `timeQuantumField` in the QuantaStream schema.

## Local Smoke

If you have a local MySQL sample database, set the DSN in your shell and run the
analyzer into a temporary directory:

```bash
MYSQL_DSN='user:password@tcp(127.0.0.1:3306)/tpch'

go run ./cmd/qstream-migrate analyze mysql \
  --dsn "$MYSQL_DSN" \
  --out /tmp/qstream-migrate-tpch \
  --sample-limit 3 \
  --query-timeout 45s
```

The first local TPC-H smoke run analyzed 8 tables, 61 fields, and produced 9
relationship candidates with no profile errors.

Generate schemas from that smoke plan:

```bash
go run ./cmd/qstream-migrate check \
  --plan /tmp/qstream-migrate-tpch/plan.yaml

go run ./cmd/qstream-migrate generate \
  --plan /tmp/qstream-migrate-tpch/plan.yaml \
  --out /tmp/qstream-migrate-tpch-config
```

Generate an editable load runbook from the same reviewed plan:

```bash
go run ./cmd/qstream-migrate load-plan \
  --plan /tmp/qstream-migrate-tpch/plan.yaml \
  --out /tmp/qstream-migrate-tpch-load \
  --relationship-mode all \
  --loader-target http://127.0.0.1:8088/ingest/json \
  --batch-size 2000
```

The load plan contains:

- `load-order.txt`: parent-before-child table order based on the selected
  relationship mode;
- `queries/<table>.json.sql`: MySQL export SQL that emits one QuantaStream JSON
  event per source row;
- `scripts/export-jsonl.sh`: exports each table to JSONL using the MySQL CLI;
- `scripts/post-jsonl.sh`: batches JSONL into the QuantaStream loader.

Future commands are expected to build on the generated schemas and plan:

```bash
qstream-migrate load \
  --plan ./migration-plan/plan.yaml \
  --target http://127.0.0.1:8088/ingest/json

qstream-migrate validate \
  --plan ./migration-plan/plan.yaml \
  --mysql-dsn 'user:pass@tcp(127.0.0.1:3306)/dbname' \
  --qs-dsn 'qstream@tcp(127.0.0.1:4000)/quanta'
```

The exact commands may change as the tool hardens.

## Analyzer Lite

The analyzer is the heart of the project. MySQL DDL can tell us column names,
declared types, keys, and relationships. It cannot tell us enough about how the
data should be represented in QuantaStream.

The first practical analyzer should gather facts such as:

- row count;
- null count;
- min and max values;
- distinct count;
- maximum string length;
- approximate p95 and p99 string length;
- sample values;
- uniqueness of candidate keys;
- foreign-key candidate validation;
- date and timestamp ranges;
- decimal scale;
- likely time-partitioning candidates;
- whether a string behaves like an identifier, a dimension, or free text.

Those facts can drive QuantaStream mapping recommendations.

## Mapping Recommendations

Initial rules of thumb:

- low-cardinality dimensions should usually map to `StringEnum`;
- identifiers, join keys, and high-cardinality exact/prefix lookup fields should
  usually map to `StringLexBSI`;
- free-text fields should be called out separately for explicit text-search
  modeling;
- integer fields should usually map to `IntBSI`;
- date and timestamp fields should usually map to `TimestampBSI`;
- money and fixed-scale decimal values should usually map to `FloatScaleBSI`;
- relationships should be generated only when source metadata or profiling can
  prove the parent/child relationship is sound enough to model.
- tables with date/time columns should be reviewed for `timeQuantumType` and
  `timeQuantumField` before loading larger data sets.

These are recommendations, not magic. The generated plan should be editable.

## What This Project Is Not

This project is not intended to migrate the full MySQL operational surface into
QuantaStream. It should not try to translate stored procedures, triggers,
transaction semantics, rollback behavior, or full DDL parity.

Those features matter in MySQL, but they are outside the first QuantaStream
migration use case.

## Repository Boundaries

The intended QuantaStream ecosystem split is:

- `quantastream`: the database engine, loader, SQL compatibility, and core
  runtime;
- `quantastream-tableau`: Tableau compatibility lab assets, runbooks, and
  captured SQL fixtures;
- `radiosport-data-lab`: a domain toolkit and showcase for contest and
  propagation analytics;
- `qstream-migrate`: migration analysis, schema generation, load orchestration,
  and validation for existing databases.

## Provenance And Licensing

This repository is a new QuantaStream companion project started in 2026 by Guy
Anthony Molinari.

It is licensed under the MIT License. See [LICENSE](LICENSE).

QuantaStream itself is a separate project and is not licensed under MIT; the
QuantaStream engine is licensed separately under the Elastic License 2.0. This
repository may generate QuantaStream schema files and interact with
QuantaStream's public interfaces, but it should not copy QuantaStream engine
implementation code.

This repository is not a fork of Disney/Quanta, and no Disney/Quanta source
code is intentionally included at repository creation.

Third-party product names such as MySQL and Tableau are used only to describe
interoperability targets. They are trademarks of their respective owners, and no
endorsement is implied.
