package bencoding

import (
	"strconv"
	"strings"
)


func ParseString(data string) (string, string, error) {
	colonIndex := strings.IndexByte(data, ':')
	if colonIndex == -1 {
		return "", "", ErrNoColonInString
	}

	// Validate all characters before colon are ASCII digits
	for i := range colonIndex {
		if data[i] < '0' || data[i] > '9' {
			return "", "", ErrNonDigitInLength
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
		return "", "", ErrStringLengthExceeds
	}

	// Return a copy of the string and remaining data
	return string([]byte(data[start:end])), data[end:], nil
}

func ParseInteger(data string) (int64, string, error) {
	if len(data) < 3 {
		return 0, "", ErrIntegerTooShort
	}

	if data[0] != 'i' {
		return 0, "", ErrIntegerNoStartMarker
	}

	eIndex := strings.IndexByte(data, 'e')
	if eIndex == -1 {
		return 0, "", ErrIntegerNoEndMarker
	}

	numStr := data[1:eIndex]

	// Validate no leading zeros (except for "0" itself)
	if len(numStr) > 1 && numStr[0] == '0' {
		return 0, "", ErrIntegerLeadingZeros
	}

	// Validate no negative zero
	if numStr == "-0" {
		return 0, "", ErrIntegerNegativeZero
	}

	i, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, "", err
	}

	return i, data[eIndex+1:], nil
}

// ParseBencodedObject parses any bencoded value (string, integer, list, or dict) from the input data.
func ParseBencodedObject(data string) (BencodedObject, string, error) {
	if len(data) == 0 {
		return BencodedObject{}, "", ErrEmptyData
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
		return nil, "", ErrListTooShort
	}

	if data[0] != 'l' {
		return nil, "", ErrListNoStartMarker
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
	return nil, "", ErrListNoEndMarker
}

// ParseDict recursively parses a Bencoded dict of objects from a string
func ParseDict(data string) (map[string]BencodedObject, string, error) {
	if len(data) < 2 {
		return nil, "", ErrDictTooShort
	}

	if data[0] != 'd' {
		return nil, "", ErrDictNoStartMarker
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

	return nil, "", ErrDictNoEndMarker

}

// ExtractInfoDictBytes extracts the raw bencoded bytes of the "info" dictionary
// from a torrent metainfo file. This is needed to calculate the info hash.
func ExtractInfoDictBytes(data string) ([]byte, error) {
	if len(data) < 2 || data[0] != 'd' {
		return nil, ErrNotADict
	}

	pos := 1 // skip 'd'

	for pos < len(data) {
		if data[pos] == 'e' {
			return nil, ErrInfoKeyNotFound
		}

		// Parse the key
		key, remaining, err := ParseString(data[pos:])
		if err != nil {
			return nil, err
		}

		// Calculate how many bytes the key consumed
		keyLen := len(data[pos:]) - len(remaining)
		pos += keyLen

		// Is this the "info" key?
		if key == "info" {
			infoStart := pos

			// Parse value to find where it ends
			_, remaining, err := ParseBencodedObject(data[pos:])
			if err != nil {
				return nil, err
			}

			// Calculate how many bytes the value consumed
			valueLen := len(data[pos:]) - len(remaining)
			infoEnd := pos + valueLen

			return []byte(data[infoStart:infoEnd]), nil
		}

		// Not "info", skip the value
		_, remaining, err = ParseBencodedObject(data[pos:])
		if err != nil {
			return nil, err
		}

		valueLen := len(data[pos:]) - len(remaining)
		pos += valueLen
	}

	return nil, ErrInfoKeyNotFound

}
