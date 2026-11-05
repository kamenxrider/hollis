package runner

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestNativeToolContract(t *testing.T) {
	data, err := os.ReadFile("../../scripts/native/testdata/native-tools-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Base  json.RawMessage
		Cases []struct {
			Name    string
			Valid   bool
			Changes [][]json.RawMessage
		}
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	for _, test := range corpus.Cases {
		t.Run(test.Name, func(t *testing.T) {
			var root any
			decoder := json.NewDecoder(bytes.NewReader(corpus.Base))
			decoder.UseNumber()
			if err := decoder.Decode(&root); err != nil {
				t.Fatal(err)
			}
			for _, change := range test.Changes {
				node := root
				for _, key := range change[:len(change)-2] {
					var field string
					if json.Unmarshal(key, &field) == nil {
						node = node.(map[string]any)[field]
					} else {
						var index int
						json.Unmarshal(key, &index)
						node = node.([]any)[index]
					}
				}
				var value any
				d := json.NewDecoder(bytes.NewReader(change[len(change)-1]))
				d.UseNumber()
				if err := d.Decode(&value); err != nil {
					t.Fatal(err)
				}
				key := change[len(change)-2]
				var field string
				if json.Unmarshal(key, &field) == nil {
					node.(map[string]any)[field] = value
				} else {
					var index int
					json.Unmarshal(key, &index)
					list := node.([]any)
					if index == len(list) {
						root.(map[string]any)["history"] = append(list, value)
					} else {
						list[index] = value
					}
				}
			}
			encoded, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			var request NativeToolStep
			err = json.Unmarshal(encoded, &request)
			if err == nil {
				err = request.validate()
			}
			if (err == nil) != test.Valid {
				t.Fatalf("valid=%v, error=%v", test.Valid, err)
			}
		})
	}
}
