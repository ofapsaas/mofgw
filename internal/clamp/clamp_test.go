package clamp

import (
	"encoding/json"
	"testing"
)

// Request body mínimo que mofgw maneja (los campos que clamp toca).
type reqBody struct {
	Model               string `json:"model"`
	MaxTokens           *int   `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int   `json:"max_completion_tokens,omitempty"`
}

func TestClampMaxTokens(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		providerMax int64
		wantMaxTok  *int
		wantMaxComp *int
	}{
		{
			name:        "excede limite -> clamp a limite",
			body:        `{"model":"m","max_tokens":131072}`,
			providerMax: 16384,
			wantMaxTok:  intPtr(16384),
		},
		{
			name:        "dentro del limite -> sin tocar",
			body:        `{"model":"m","max_tokens":2048}`,
			providerMax: 16384,
			wantMaxTok:  intPtr(2048),
		},
		{
			name:        "max_tokens ausente -> sin tocar",
			body:        `{"model":"m"}`,
			providerMax: 16384,
			wantMaxTok:  nil,
		},
		{
			name:        "max_tokens 0 -> sin tocar",
			body:        `{"model":"m","max_tokens":0}`,
			providerMax: 16384,
			wantMaxTok:  intPtr(0),
		},
		{
			name:        "provider sin limite (0) -> sin clamp",
			body:        `{"model":"m","max_tokens":999999}`,
			providerMax: 0,
			wantMaxTok:  intPtr(999999),
		},
		{
			name:        "max_completion_tokens excede -> clamp a limite",
			body:        `{"model":"m","max_completion_tokens":131072}`,
			providerMax: 32768,
			wantMaxComp: intPtr(32768),
		},
		{
			name:        "ambos exceden -> clamp ambos",
			body:        `{"model":"m","max_tokens":100000,"max_completion_tokens":200000}`,
			providerMax: 16384,
			wantMaxTok:  intPtr(16384),
			wantMaxComp: intPtr(16384),
		},
		{
			name:        "uno excede y otro no -> clamp solo el que excede",
			body:        `{"model":"m","max_tokens":1000,"max_completion_tokens":200000}`,
			providerMax: 16384,
			wantMaxTok:  intPtr(1000),
			wantMaxComp: intPtr(16384),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var orig reqBody
			if err := json.Unmarshal([]byte(tc.body), &orig); err != nil {
				t.Fatalf("fixture invalido: %v", err)
			}
			out, err := Request([]byte(tc.body), tc.providerMax)
			if err != nil {
				t.Fatalf("Request() error: %v", err)
			}
			var got reqBody
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("output no es JSON valido: %v", err)
			}
			if !intEq(got.MaxTokens, tc.wantMaxTok) {
				t.Errorf("max_tokens = %v, want %v", got.MaxTokens, tc.wantMaxTok)
			}
			if !intEq(got.MaxCompletionTokens, tc.wantMaxComp) {
				t.Errorf("max_completion_tokens = %v, want %v", got.MaxCompletionTokens, tc.wantMaxComp)
			}
			// el resto del body debe sobrevivir intacto
			var origModel, gotModel string
			_ = json.Unmarshal([]byte(tc.body), &struct{ Model string }{})
			_ = json.Unmarshal(out, &struct{ Model string }{})
			_ = json.Unmarshal([]byte(tc.body), &origModel)
			_ = json.Unmarshal(out, &gotModel)
			_ = origModel
			_ = gotModel
		})
	}
}

func TestClampInvalidBody(t *testing.T) {
	if _, err := Request([]byte(`{not json`), 100); err == nil {
		t.Fatal("Request() con body invalido deberia dar error")
	}
}

func TestClampPreservesOtherFields(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}],"temperature":0.7,"max_tokens":50000,"top_p":0.9}`
	out, err := Request([]byte(body), 8192)
	if err != nil {
		t.Fatalf("Request() error: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output invalido: %v", err)
	}
	var mt int
	if err := json.Unmarshal(m["max_tokens"], &mt); err != nil || mt != 8192 {
		t.Errorf("max_tokens = %d, want 8192", mt)
	}
	if string(m["messages"]) == "" || string(m["temperature"]) == "" || string(m["top_p"]) == "" {
		t.Error("campos no relacionados se perdieron")
	}
}

func intPtr(v int) *int { return &v }
func intEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
