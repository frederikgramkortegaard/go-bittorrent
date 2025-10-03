package bencoding

import "fmt"

// PrintObject prints a BencodedObject with proper indentation
func PrintObject(obj BencodedObject, indent int) {
	prefix := ""
	for i := 0; i < indent; i++ {
		prefix += "  "
	}

	switch obj.Typ {
	case BenString:
		if obj.StrVal != nil {
			fmt.Printf("%s\n", *obj.StrVal)
		}
	case BenInteger:
		if obj.IntVal != nil {
			fmt.Printf("%d\n", *obj.IntVal)
		}
	case BenList:
		fmt.Println("[")
		for _, item := range obj.List {
			fmt.Print(prefix + "  ")
			PrintObject(item, indent+1)
		}
		fmt.Println(prefix + "]")
	case BenDict:
		fmt.Println("{")
		PrintDict(obj.Dict, indent+1)
		fmt.Println(prefix + "}")
	}
}

// PrintDict prints a bencoded dictionary with proper indentation
func PrintDict(dict map[string]BencodedObject, indent int) {
	prefix := ""
	for i := 0; i < indent; i++ {
		prefix += "  "
	}

	for key, val := range dict {
		fmt.Printf("%s%s: ", prefix, key)
		PrintObject(val, indent)
	}
}
