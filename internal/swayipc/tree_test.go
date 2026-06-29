package swayipc

import (
	"encoding/json"
	"testing"
)

func TestFindFocused(t *testing.T) {
	raw := []byte(`{
	  "id": 1, "type": "root", "nodes": [{
	    "id": 2, "type": "output", "name": "HDMI-A-1", "nodes": [{
	      "id": 3, "type": "workspace", "name": "1", "nodes": [{
	        "id": 4, "type": "con", "name": "editor", "app_id": "code", "pid": 42,
	        "focused": true, "window_properties": {"class": "Code"}
	      }]
	    }]
	  }]
	}`)
	var root Node
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	f, ok := FindFocused(root)
	if !ok {
		t.Fatal("focused node not found")
	}
	if f.ID != 4 || f.AppID != "code" || f.Class != "Code" || f.Workspace != "1" || f.Output != "HDMI-A-1" {
		t.Fatalf("bad focus: %+v", f)
	}
}
