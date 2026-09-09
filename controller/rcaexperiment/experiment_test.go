package rcaexperimentcontroller

import (
	"GopherAI/internal/rcaexperiment"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEndpointsValidateIdentityAndBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	d, e := rcaexperiment.Load()
	if e != nil {
		t.Fatal(e)
	}
	h := NewHandler(d, nil, nil)
	for _, tt := range []struct {
		name, body, user string
		want             int
	}{
		{"unauthenticated", `{}`, "", 401},
		{"forged answer", `{"case_id":"x","strategy":"case_based","fault":"cpu"}`, "tester", 400},
		{"unknown case", `{"case_id":"no-such-case","strategy":"case_based"}`, "tester", 404},
		{"unknown strategy", `{"case_id":"x","strategy":"repair"}`, "tester", 400},
		{"valid", `{"case_id":"` + d.Catalog[6].ID + `","strategy":"case_based"}`, "tester", 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("userName", tt.user)
			c.Request = httptest.NewRequest("POST", "/diagnose", strings.NewReader(tt.body))
			h.Diagnose(c)
			if w.Code != tt.want {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestSingleConcurrencyGuard(t *testing.T) {
	d, e := rcaexperiment.Load()
	if e != nil {
		t.Fatal(e)
	}
	h := NewHandler(d, nil, nil)
	h.gate <- struct{}{}
	defer func() { <-h.gate }()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("userName", "tester")
	c.Request = httptest.NewRequest("POST", "/diagnose", strings.NewReader(`{"case_id":"`+d.Catalog[6].ID+`","strategy":"case_based"}`))
	h.Diagnose(c)
	if w.Code != 429 {
		t.Fatal(w.Code)
	}
}
