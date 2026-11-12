package feishu

import (
	"encoding/json"
	"testing"
)

func TestUserAttrs(t *testing.T) {
	var u User
	raw := `{"open_id":"ou-1","job_title":"架构师","city":"上海","employee_no":"E01","employee_type":1,
	  "custom_attrs":[{"type":"TEXT","id":"C-level","value":{"text":"核心"}},{"type":"ENUMERATION","id":"C-proj","value":{"option_value":"A"}},{"id":"C-empty","value":{}}]}`
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	a := u.Attrs()
	if a["job_title"][0] != "架构师" || a["city"][0] != "上海" || a["employee_no"][0] != "E01" || a["employee_type"][0] != "1" {
		t.Fatalf("标准属性：%v", a)
	}
	if a["C-level"][0] != "核心" || a["C-proj"][0] != "A" {
		t.Fatalf("自定义字段：%v", a)
	}
	if _, ok := a["C-empty"]; ok {
		t.Fatalf("空值不该出现：%v", a)
	}
}
