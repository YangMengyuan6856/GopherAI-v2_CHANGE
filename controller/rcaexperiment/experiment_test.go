package rcaexperimentcontroller

import (
	"GopherAI/internal/rcaagent"
	"GopherAI/internal/rcaexperiment"
	"context"
	"encoding/json"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
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

type failingAgentModel struct{}

func (failingAgentModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage("invalid", nil), nil
}

func TestAutonomousFailureRemainsVisibleAndIsNotAValidRejection(t *testing.T) {
	d, e := rcaexperiment.Load()
	if e != nil {
		t.Fatal(e)
	}
	h := NewHandler(d, nil, nil)
	h.agentFactory = func(context.Context) (*rcaagent.Agent, error) {
		return rcaagent.New(failingAgentModel{}, "controller-test-double")
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("userName", "tester")
	c.Request = httptest.NewRequest("POST", "/diagnose", strings.NewReader(`{"case_id":"`+d.Catalog[6].ID+`","strategy":"autonomous"}`))
	h.Diagnose(c)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"evaluation_valid":false`) || !strings.Contains(w.Body.String(), `"stop_reason":"invalid_model_output"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"strategy":"case_based"`) {
		t.Fatal("silent rule fallback")
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

func TestFrozenAgentReplayAndRecordedTraceAreVersionBound(t *testing.T) {
	t.Chdir("../..")
	d, e := rcaexperiment.Load()
	if e != nil {
		t.Fatal(e)
	}
	h := NewHandler(d, nil, nil) // No model factory: viewing records cannot call a model.
	for _, tt := range []struct {
		id   string
		want int
	}{{"", 200}, {"rca-34a5398b52", 200}, {"not-in-report", 404}} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("userName", "tester")
		c.Params = gin.Params{{Key: "id", Value: tt.id}}
		c.Request = httptest.NewRequest("GET", "/agent-report/"+tt.id, nil)
		h.AgentReport(c)
		if w.Code != tt.want {
			t.Fatalf("report %s: %d %s", tt.id, w.Code, w.Body.String())
		}
		if tt.want != 200 {
			continue
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal("invalid report JSON")
		}
		if tt.id == "" {
			var rows []json.RawMessage
			if json.Unmarshal(body["cases"], &rows) != nil || len(rows) != 12 {
				t.Fatal("incomplete replay")
			}
		} else if string(body["recorded"]) != "true" || len(body["run"]) == 0 {
			t.Fatal("record presented as fresh execution")
		}
	}
}
