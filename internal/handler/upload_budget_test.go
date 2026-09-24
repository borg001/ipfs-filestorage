package handler

import (
	"net/http/httptest"
	"testing"
)

// One session uploads at most its daily budget; another session has its own.
func TestUploadBudgetBoundsASessionPerDay(t *testing.T) {
	budget := newUploadBudget(100)
	first := httptest.NewRequest("POST", "/upload", nil)
	first.Header.Set("Authorization", "Bearer first-session")
	second := httptest.NewRequest("POST", "/upload", nil)
	second.Header.Set("Authorization", "Bearer second-session")
	if !budget.take(uploadSessionKey(first), 60) || budget.take(uploadSessionKey(first), 60) {
		t.Fatal("the first session went past its budget")
	}
	if !budget.take(uploadSessionKey(second), 60) {
		t.Fatal("the second session was charged for the first")
	}
	var none *uploadBudget
	if !none.take("any", 1<<40) {
		t.Fatal("a missing budget refused an upload")
	}
}
