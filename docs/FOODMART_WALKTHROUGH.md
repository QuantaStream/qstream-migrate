# FoodMart Star-Schema Walkthrough

FoodMart is a compact analytical warehouse with sales facts and conventional
customer, product, promotion, store, and time dimensions. It is useful for
exercising `qstream-migrate` against a schema that resembles a real BI model.

FoodMart is not distributed by this repository. The data package used here is
the Apache-2.0-licensed
[`julianhyde/foodmart-data-mysql`](https://github.com/julianhyde/foodmart-data-mysql)
project. The compatible MySQL table definitions come from
[`rav009/foodmart-for-mysql`](https://github.com/rav009/foodmart-for-mysql).
Review those projects and their licenses before use.

## 1. Download the source files

```bash
git clone --depth 1 https://github.com/julianhyde/foodmart-data-mysql.git \
  /tmp/foodmart-data-mysql
git clone --depth 1 https://github.com/rav009/foodmart-for-mysql.git \
  /tmp/foodmart-for-mysql
```

## 2. Create and load an isolated MySQL database

Run this with a MySQL administrative account, substituting your migration
account and host as needed:

```sql
CREATE DATABASE foodmart CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
GRANT ALL PRIVILEGES ON foodmart.* TO 'bench'@'127.0.0.1';
```

The schema file uses uppercase `FOODMART`. Normalize that database reference
when loading on Linux, where database names are commonly case-sensitive:

```bash
sed 's/^USE FOODMART;/USE foodmart;/' \
  '/tmp/foodmart-for-mysql/foodmart schema.sql' |
mysql -h 127.0.0.1 -P 3306 -u bench -p foodmart
```

Load the 65 MB data script in one transaction. The upstream script contains
roughly 328,000 individual inserts, so allowing per-row autocommit is much
slower.

```bash
mysql --init-command="SET SESSION sql_mode='ANSI_QUOTES'; SET autocommit=0" \
  -h 127.0.0.1 -P 3306 -u bench -p foodmart \
  -e "source /tmp/foodmart-data-mysql/src/main/resources/data.sql; COMMIT;"
```

The optional upstream `after.sql` builds Mondrian aggregate tables. It is not
required because QuantaStream will operate on the base star.

## 3. Validate and declare the star

The commonly available FoodMart schema declares no primary or foreign keys.
QuantaStream needs stable primary keys and relationship metadata for its
intended parent-child execution path.

Before adding metadata, verify dimension IDs are unique:

```sql
SELECT COUNT(*), COUNT(DISTINCT customer_id) FROM customer;
SELECT COUNT(*), COUNT(DISTINCT product_id) FROM product;
SELECT COUNT(*), COUNT(DISTINCT product_class_id) FROM product_class;
SELECT COUNT(*), COUNT(DISTINCT promotion_id) FROM promotion;
SELECT COUNT(*), COUNT(DISTINCT store_id) FROM store;
SELECT COUNT(*), COUNT(DISTINCT time_id) FROM time_by_day;
```

Verify each fact edge has no orphans, substituting each of `product_id`,
`time_id`, `customer_id`, `promotion_id`, and `store_id`:

```sql
SELECT COUNT(*) AS orphan_products
FROM sales_fact_1997 f LEFT JOIN product d USING (product_id)
WHERE d.product_id IS NULL;
```

Each result must be zero. Then apply the preparation script:

```bash
mysql -h 127.0.0.1 -P 3306 -u bench -p foodmart \
  < examples/foodmart/prepare-star.sql
```

The script preserves dimension IDs and adds a generated `sales_fact_id` to the
fact table. This is staging remediation; do not alter a production source
without reviewing the migration design and operational consequences.

## 4. Analyze and generate

```bash
go run ./cmd/qstream-migrate analyze mysql \
  --dsn 'bench:YOUR_PASSWORD@tcp(127.0.0.1:3306)/foodmart' \
  --schema foodmart \
  --tables customer,product_class,product,promotion,store,time_by_day,sales_fact_1997 \
  --out migration-plan-foodmart

go run ./cmd/qstream-migrate check \
  --plan migration-plan-foodmart/plan.yaml \
  --relationship-mode metadata

go run ./cmd/qstream-migrate generate \
  --plan migration-plan-foodmart/plan.yaml \
  --relationship-mode metadata \
  --out migration-plan-foodmart/configuration

go run ./cmd/qstream-migrate load-plan \
  --plan migration-plan-foodmart/plan.yaml \
  --relationship-mode metadata \
  --out migration-plan-foodmart/load
```

The expected load order places all six dimensions before `sales_fact_1997`.
Review generated mapper choices and time-partitioning warnings before loading.
Dates on dimensions are not automatically a reason to partition those tables,
and the fact contains a `time_id` rather than a physical timestamp.

## Observed reference counts

The source revisions tested on September 3, 2026 produced 36 tables overall.
The focused star contained 86,837 rows in `sales_fact_1997`, 10,281 customers,
1,560 products, 1,864 promotions, 25 stores, 730 dates, and 110 product classes.
Upstream revisions may produce different counts.
