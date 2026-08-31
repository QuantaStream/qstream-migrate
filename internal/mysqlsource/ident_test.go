package mysqlsource

import "testing"

func TestQuoteIdent(t *testing.T) {
	got := quoteIdent("we`ird")
	if got != "`we``ird`" {
		t.Fatalf("quoteIdent = %q", got)
	}
}

func TestQualifiedTable(t *testing.T) {
	got := qualifiedTable("sales", "orders")
	if got != "`sales`.`orders`" {
		t.Fatalf("qualifiedTable = %q", got)
	}
}

func TestPercentileLength(t *testing.T) {
	buckets := []lengthBucket{
		{length: 4, count: 50},
		{length: 8, count: 45},
		{length: 12, count: 5},
	}
	if got := percentileLength(buckets, 100, 0.95); got != 8 {
		t.Fatalf("p95 = %d, want 8", got)
	}
	if got := percentileLength(buckets, 100, 0.99); got != 12 {
		t.Fatalf("p99 = %d, want 12", got)
	}
}
