package agent

import (
	"encoding/json"
	"slices"
	"testing"
)

// Рядки у форматі `claude -p --output-format stream-json --verbose`.
func TestDescribe(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{`{"type":"system","subtype":"init","session_id":"x"}`, nil},
		{`{"type":"assistant","message":{"content":[{"type":"text","text":"Дивлюсь\nкод"}]}}`,
			[]string{"💬 Дивлюсь код"}},
		{`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./...","description":"run tests"}}]}}`,
			[]string{"🔧 Bash: go test ./..."}},
		{`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/main.go","old_string":"a","new_string":"b"}}]}}`,
			[]string{"🔧 Edit: /w/main.go"}},
		{`{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"exit 1"}]}}`,
			[]string{`⚠️ помилка інструмента: "exit 1"`}},
		{`{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}`, nil},
	}
	for _, c := range cases {
		var ev event
		if err := json.Unmarshal([]byte(c.line), &ev); err != nil {
			t.Fatal(err)
		}
		if got := describe(&ev); !slices.Equal(got, c.want) {
			t.Errorf("describe(%s) = %q, очікував %q", c.line, got, c.want)
		}
	}
}

func TestResultEvent(t *testing.T) {
	var ev event
	line := `{"type":"result","subtype":"success","is_error":false,"result":"Готово\nVERDICT: APPROVE","total_cost_usd":0.42,"num_turns":7}`
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != "result" || ev.Result != "Готово\nVERDICT: APPROVE" || ev.NumTurns != 7 || ev.TotalCostUSD != 0.42 {
		t.Errorf("неправильно розібрано: %+v", ev)
	}
}
