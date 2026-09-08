package humanreviewimport

import "testing"

func TestFullIdempotencyKeyIsCatalogLineageScoped(t *testing.T) {
	source := "0a228ed89eca7ae5c5c3d1ae4efdbc89e4d8fed7bef7a05c2d7f2fb949c8b56a"
	oldCatalog := "6f5cff1cfb3c0c683ac5241fd9ceb117337833085626e4b8785d80f8b7108cd7"
	newCatalog := "69e5d1d9ea6102e2fdb12f23ebd0303a175e8d8f4e744a2edb3387ad422e7bb7"
	oldGovernance := "69e4bebb40d8af63ead5de728c267a4bac284725cf1db52ba5ee07001567accf"
	newGovernance := "a9e4bebb40d8af63ead5de728c267a4bac284725cf1db52ba5ee07001567accf"
	oldKey := fullIdempotencyKey(source, oldCatalog, oldGovernance, 1)
	newKey := fullIdempotencyKey(source, newCatalog, oldGovernance, 1)
	if oldKey == newKey {
		t.Fatal("idempotency key must change with the destination catalog")
	}
	if newKey != "human-review-0a228ed89e-69e5d1d9ea-69e4bebb40-full-001" {
		t.Fatalf("unexpected stable key: %s", newKey)
	}
	if fullIdempotencyKey(source, newCatalog, oldGovernance, 1) == fullIdempotencyKey(source, newCatalog, newGovernance, 1) {
		t.Fatal("idempotency key must change with the destination governance")
	}
}
