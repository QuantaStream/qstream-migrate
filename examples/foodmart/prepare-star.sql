-- Prepare the classic FoodMart 1997 sales star for metadata-driven migration.
-- Run only after confirming the validation queries in the FoodMart walkthrough.

ALTER TABLE customer ADD PRIMARY KEY (customer_id);
ALTER TABLE product_class ADD PRIMARY KEY (product_class_id);

ALTER TABLE product
  ADD PRIMARY KEY (product_id),
  ADD CONSTRAINT fk_product_class
    FOREIGN KEY (product_class_id) REFERENCES product_class(product_class_id);

ALTER TABLE promotion ADD PRIMARY KEY (promotion_id);
ALTER TABLE store ADD PRIMARY KEY (store_id);
ALTER TABLE time_by_day ADD PRIMARY KEY (time_id);

ALTER TABLE sales_fact_1997
  ADD COLUMN sales_fact_id BIGINT NOT NULL AUTO_INCREMENT FIRST,
  ADD PRIMARY KEY (sales_fact_id),
  ADD CONSTRAINT fk_sales_product
    FOREIGN KEY (product_id) REFERENCES product(product_id),
  ADD CONSTRAINT fk_sales_time
    FOREIGN KEY (time_id) REFERENCES time_by_day(time_id),
  ADD CONSTRAINT fk_sales_customer
    FOREIGN KEY (customer_id) REFERENCES customer(customer_id),
  ADD CONSTRAINT fk_sales_promotion
    FOREIGN KEY (promotion_id) REFERENCES promotion(promotion_id),
  ADD CONSTRAINT fk_sales_store
    FOREIGN KEY (store_id) REFERENCES store(store_id);
