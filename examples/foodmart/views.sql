CREATE VIEW foodmart_sales_base AS
SELECT
  f.sales_fact_id,
  f.product_id,
  f.time_id,
  f.customer_id,
  f.promotion_id,
  f.store_id,
  t.the_date AS sales_date,
  t.the_day AS sales_day,
  t.the_month AS sales_month,
  t.the_year AS sales_year,
  t.quarter AS sales_quarter,
  t.week_of_year,
  c.fullname AS customer_name,
  c.city AS customer_city,
  c.state_province AS customer_state,
  c.country AS customer_country,
  c.gender,
  c.marital_status,
  c.yearly_income,
  c.education,
  c.occupation,
  c.member_card,
  p.product_name,
  p.brand_name,
  pc.product_subcategory,
  pc.product_category,
  pc.product_department,
  pc.product_family,
  pr.promotion_name,
  pr.media_type AS promotion_media_type,
  pr.cost AS promotion_cost,
  s.store_name,
  s.store_type,
  s.store_city,
  s.store_state,
  s.store_country,
  f.store_sales,
  f.store_cost,
  f.unit_sales,
  f.store_sales - f.store_cost AS gross_profit
FROM sales_fact_1997 f
INNER JOIN time_by_day t ON f.time_id = t.time_id
INNER JOIN customer c ON f.customer_id = c.customer_id
INNER JOIN product p ON f.product_id = p.product_id
INNER JOIN product_class pc ON p.product_class_id = pc.product_class_id
INNER JOIN promotion pr ON f.promotion_id = pr.promotion_id
INNER JOIN store s ON f.store_id = s.store_id;
