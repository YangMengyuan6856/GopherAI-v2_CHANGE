package user

import (
	"reflect"
	"testing"
)

func TestLoginRequestPasswordJSONTag(t *testing.T) {
	field, ok := reflect.TypeOf(LoginRequest{}).FieldByName("Password")
	if !ok {
		t.Fatal("Password field is missing")
	}
	if got := field.Tag.Get("json"); got != "password" {
		t.Fatalf("expected password JSON tag, got %q", got)
	}
}
