package bencoding

import "testing"

func TestParseString(t *testing.T) {
	str, remaining, err := ParseString("4:spam")
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}
	if str != "spam" {
		t.Errorf("Expected 'spam', got '%s'", str)
	}
	if remaining != "" {
		t.Errorf("Expected empty remaining, got '%s'", remaining)
	}
}

func TestParseInteger(t *testing.T) {
	i, remaining, err := ParseInteger("i42e")
	if err != nil {
		t.Fatalf("ParseInteger failed: %v", err)
	}
	if i != 42 {
		t.Errorf("Expected 42, got %d", i)
	}
	if remaining != "" {
		t.Errorf("Expected empty remaining, got '%s'", remaining)
	}
}

func TestParseIntegerNegativeZero(t *testing.T) {
	_, _, err := ParseInteger("i-0e")
	if err == nil {
		t.Error("Expected error for negative zero, got nil")
	}
}

func TestParseIntegerLeadingZero(t *testing.T) {
	_, _, err := ParseInteger("i03e")
	if err == nil {
		t.Error("Expected error for leading zero, got nil")
	}
}

func TestParseList(t *testing.T) {
	list, remaining, err := ParseList("li1ei2ei3ee")
	if err != nil {
		t.Fatalf("ParseList failed: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("Expected list length 3, got %d", len(list))
	}
	if remaining != "" {
		t.Errorf("Expected empty remaining, got '%s'", remaining)
	}
}

func TestParseDict(t *testing.T) {
	dict, remaining, err := ParseDict("d3:cow3:moo4:spam4:eggse")
	if err != nil {
		t.Fatalf("ParseDict failed: %v", err)
	}
	if len(dict) != 2 {
		t.Errorf("Expected dict length 2, got %d", len(dict))
	}
	if remaining != "" {
		t.Errorf("Expected empty remaining, got '%s'", remaining)
	}

	cow, ok := dict["cow"]
	if !ok || cow.StrVal == nil || *cow.StrVal != "moo" {
		t.Error("Expected cow=>moo in dict")
	}
}

func TestParseNestedStructure(t *testing.T) {
	// Test complex nested bencode structure
	// {
	//   "name": "John",
	//   "age": 30,
	//   "tags": [1, 2, 3],
	//   "meta": {
	//     "created": "2024-01-01",
	//     "items": ["spam", "eggs"]
	//   }
	// }
	testData := "d" +
		"4:name" + "4:John" +
		"3:age" + "i30e" +
		"4:tags" + "l" + "i1e" + "i2e" + "i3e" + "e" +
		"4:meta" + "d" +
			"7:created" + "10:2024-01-01" +
			"5:items" + "l" + "4:spam" + "4:eggs" + "e" +
		"e" +
	"e"

	dict, remaining, err := ParseDict(testData)
	if err != nil {
		t.Fatalf("ParseDict failed: %v", err)
	}
	if len(dict) != 4 {
		t.Errorf("Expected 4 keys, got %d", len(dict))
	}
	if remaining != "" {
		t.Errorf("Expected empty remaining, got '%s'", remaining)
	}

	// Check name
	if name, ok := dict["name"]; !ok || name.StrVal == nil || *name.StrVal != "John" {
		t.Error("Expected name=>John")
	}

	// Check age
	if age, ok := dict["age"]; !ok || age.IntVal == nil || *age.IntVal != 30 {
		t.Error("Expected age=>30")
	}

	// Check tags list
	if tags, ok := dict["tags"]; !ok || len(tags.List) != 3 {
		t.Error("Expected tags list with 3 items")
	}

	// Check nested meta dict
	if meta, ok := dict["meta"]; !ok || len(meta.Dict) != 2 {
		t.Error("Expected meta dict with 2 keys")
	}
}
