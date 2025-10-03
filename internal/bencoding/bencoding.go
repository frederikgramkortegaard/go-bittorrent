package bencoding

import (
	"errors"
	"strconv"
	"strings"
)


func ParseString(data string) (string, string, error) {
	colonIndex := strings.IndexByte(data, ':')
	if colonIndex == -1 {
		return "", "", errors.New("no colon found in bencoded string")
	}

	// Validate all characters before colon are ASCII digits
	for i := range colonIndex {
		if data[i] < '0' || data[i] > '9' {
			return "", "", errors.New("expected ASCII digit in length")
		}
	}

	// Parse the length
	size, err := strconv.Atoi(data[:colonIndex])
	if err != nil {
		return "", "", err
	}

	// Check bounds and extract string
	start := colonIndex + 1
	end := start + size
	if end > len(data) {
		return "", "", errors.New("string length exceeds data bounds")
	}

	// Return a copy of the string and remaining data
	return string([]byte(data[start:end])), data[end:], nil
}

func ParseInteger(data string) (int64, string, error) {
	if len(data) < 3 {
		return 0, "", errors.New("data is too short to represent a bencoded integer")
	}

	if data[0] != 'i' {
		return 0, "", errors.New("bencoded integer does not start with 'i'")
	}

	eIndex := strings.IndexByte(data, 'e')
	if eIndex == -1 {
		return 0, "", errors.New("no 'e' terminator found")
	}

	numStr := data[1:eIndex]

	// Validate no leading zeros (except for "0" itself)
	if len(numStr) > 1 && numStr[0] == '0' {
		return 0, "", errors.New("leading zeros not allowed")
	}

	// Validate no negative zero
	if numStr == "-0" {
		return 0, "", errors.New("negative zero not allowed")
	}

	i, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, "", err
	}

	return i, data[eIndex+1:], nil
}

type BenType string

const (
	BenString  BenType = "BenString"
	BenInteger BenType = "BenInteger"
	BenList    BenType = "BenList"
	BenDict    BenType = "BenDict"
)

// BencodedObject is a dynamic type container, which allows us to have e.g. lists or dictionaries
// with values of alternating types. @NOTE : We could instead of this, use the interface approach
// and reduce the allocations (from the heap-pointers) but since most .torrent files are relatively
// small the self-documentation, compile-time type-checking, and very clear definition of this
// approach is what we're going to prefer
type BencodedObject struct {
	Typ    BenType
	IntVal *int64
	StrVal *string
	List   []BencodedObject
	Dict   map[string]BencodedObject
}

func ParseBencodedObject(data string) (BencodedObject, string, error) {
	if len(data) == 0 {
		return BencodedObject{}, "", errors.New("empty data")
	}

	var err error
	switch data[0] {
	case 'i':
		var i int64
		i, data, err = ParseInteger(data)
		if err != nil {
			return BencodedObject{}, "", err
		}
		return BencodedObject{
			Typ:    BenInteger,
			IntVal: &i,
		}, data, nil

	case 'd':
		var dict map[string]BencodedObject
		dict, data, err = ParseDict(data)
		if err != nil {
			return BencodedObject{}, "", err
		}
		return BencodedObject{
			Typ:  BenDict,
			Dict: dict,
		}, data, nil

	case 'l':
		var list []BencodedObject
		list, data, err = ParseList(data)
		if err != nil {
			return BencodedObject{}, "", err
		}
		return BencodedObject{
			Typ:  BenList,
			List: list,
		}, data, nil

	default:
		var str string
		str, data, err = ParseString(data)
		if err != nil {
			return BencodedObject{}, "", err
		}
		return BencodedObject{
			Typ:    BenString,
			StrVal: &str,
		}, data, nil
	}
}

// ParseList recursively parses a Bencoded list of objects from a given string
func ParseList(data string) ([]BencodedObject, string, error) {
	if len(data) < 2 {
		return nil, "", errors.New("data is not long enough to contain a list")
	}

	if data[0] != 'l' {
		return nil, "", errors.New("improperly formatted list, does not start with 'l'")
	}

	list := make([]BencodedObject, 0)

	data = data[1:]
	for len(data) > 0 {
		if data[0] == 'e' {
			return list, data[1:], nil
		}

		obj, remaining, err := ParseBencodedObject(data)
		if err != nil {
			return nil, "", err
		}

		list = append(list, obj)
		data = remaining
	}
	return nil, "", errors.New("list not terminated with 'e'")
}

// ParseDict recursively parses a Bencoded dict of objects from a string
func ParseDict(data string) (map[string]BencodedObject, string, error) {
	if len(data) < 2 {
		return nil, "", errors.New("data is not long enough to contain a dict")
	}

	if data[0] != 'd' {
		return nil, "", errors.New("improperly formatted dict, does not start with 'd'")
	}

	dict := make(map[string]BencodedObject)

	data = data[1:]
	for len(data) > 0 {
		if data[0] == 'e' {
			return dict, data[1:], nil
		}

		// Parse the bencoded key
		key, remaining, err := ParseString(data)
		if err != nil {
			return nil, "", err
		}
		data = remaining

		// Parse value
		obj, remaining, err := ParseBencodedObject(data)
		if err != nil {
			return nil, "", err
		}

		dict[key] = obj
		data = remaining
	}

	return nil, "", errors.New("dict not terminated with 'e'")

}
