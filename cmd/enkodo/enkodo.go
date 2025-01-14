package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const packageName = "github.com/micahjmartin/enkodo"

var tag = regexp.MustCompile("enkodo:\"(\\w+)\"")

var enc_types = map[string]string{
	"uint":    "Uint",
	"uint8":   "Uint8",
	"uint16":  "Uint16",
	"uint32":  "Uint32",
	"uint64":  "Uint64",
	"int":     "Int",
	"int8":    "Int8",
	"int16":   "Int16",
	"int32":   "Int32",
	"int64":   "Int64",
	"float32": "Float32",
	"float64": "Float64",
	"string":  "String",
	"[]byte":  "Bytes",
	"bool":    "Bool",
}

const ident = "\t"

type Field struct {
	Name         string
	Type         string
	OverrideType string
}

type Struct struct {
	Name   string
	Fields []Field

	_declared   map[string]string
	_hasLoopVar bool
}

func (s *Struct) String() string {
	return fmt.Sprintf("%s: %v", s.Name, s.Fields)
}

func (s *Struct) EncodeFunc(f io.Writer) error {
	s._declared = make(map[string]string)
	fnRef := strings.ToLower(s.Name[0:1])
	fmt.Fprintf(f, "func (%s *%s) MarshalEnkodo(enc *enkodo.Encoder) (err error) {\n", fnRef, s.Name)
	for _, field := range s.Fields {
		field.Name = fnRef + "." + field.Name
		s.EncodeField(1, field, f)
	}
	fmt.Fprintf(f, ident+"return\n}\n\n")
	return nil
}

func (s *Struct) DecodeFunc(f io.Writer) error {
	fnRef := strings.ToLower(s.Name[0:1])
	fmt.Fprintf(f, "func (%s *%s) UnmarshalEnkodo(dec *enkodo.Decoder) (err error) {\n", fnRef, s.Name)
	for _, field := range s.Fields {
		field.Name = fnRef + "." + field.Name
		s.DecodeField(1, field, f)
	}
	fmt.Fprint(f, ident+"return\n}\n\n")
	return nil
}

func (s *Struct) EncodeField(identCount int, field Field, f io.Writer) (err error) {
	dent := strings.Repeat(ident, identCount)
	name := field.Name
	if field.OverrideType != "" {
		name = fmt.Sprintf("%s(%s)", field.OverrideType, field.Name)
		field.Type = field.OverrideType
	}

	if field.Type == "" || field.Type[0] == '[' && len(field.Type) == 2 {
		fmt.Fprintf(f, "%s// Do not know what to do with %s (%s)\n", dent, field.Name, field.Type)
		return
	}

	if result, ok := enc_types[field.Type]; ok {
		fmt.Fprintf(f, "%senc.%s(%s)\n", dent, result, name)
		return
	}

	// Handle pointers to other types
	if field.Type[0] == '*' {
		fmt.Fprintf(f, "%senc.Encode(%s)\n", dent, name)
		return
	}

	// Handle arrays
	if field.Type[0] == '[' {
		fmt.Fprintf(f, "%senc.Int(len(%s))\n", dent, name)
		fmt.Fprintf(f, "%sfor _, v := range %s {\n", dent, name)
		if err := s.EncodeField(identCount+1, Field{Name: "v", Type: field.Type[2:]}, f); err != nil {
			return err
		}
		fmt.Fprintln(f, dent+"}")
		return
	}

	fmt.Fprintf(f, "%s// Do not know what to do with %s (%s)\n", dent, field.Name, field.Type)
	return nil
}

func (s *Struct) DecodeField(identCount int, field Field, f io.Writer) (err error) {
	dent := strings.Repeat(ident, identCount)
	name := field.Name
	var ogType string
	if field.OverrideType != "" {
		ogType = field.Type
		field.Type = field.OverrideType
	}

	if field.Type == "" || field.Type[0] == '[' && len(field.Type) == 2 {
		fmt.Fprintf(f, "%s// Do not know what to do with %s (%s)\n", dent, field.Name, field.Type)
		return
	}
	// bytes is a special case for decode because we need to build the array
	if field.Type == "[]byte" {
		fmt.Fprintf(f, "%s%s = make([]byte, 0)\n", dent, name)
		fmt.Fprintf(f, "%sif err = dec.Bytes(&%s); err != nil {\n", dent, name)
		fmt.Fprintf(f, "%sreturn\n%s}\n", dent+ident, dent)
		return
	}

	// These basic functions are all error wrapped
	if result, ok := enc_types[field.Type]; ok {
		// Special case for overrides where we assign it to a different value, then set it in the obj
		init, varName := initType(field.Type)
		if field.OverrideType != "" {
			if _, ok := s._declared[varName]; !ok {
				s._declared[varName] = field.Type
				fmt.Fprintf(f, "%s%s\n", dent, init)
			}

			fmt.Fprintf(f, "%sif %s, err = dec.%s(); err != nil {\n", dent, varName, result)
			fmt.Fprintf(f, "%sreturn\n%s}\n", dent+ident, dent)
			fmt.Fprintf(f, "%s%s = %s(%s)\n", dent, name, ogType, varName)
		} else {
			fmt.Fprintf(f, "%sif %s, err = dec.%s(); err != nil {\n", dent, name, result)
			fmt.Fprintf(f, "%sreturn\n%s}\n", dent+ident, dent)
		}
		return
	}

	// Handle pointers to other types
	if field.Type[0] == '*' {
		fmt.Fprintf(f, "%s%s = new(%s)\n", dent, name, strings.Trim(field.Type, "*"))
		fmt.Fprintf(f, "%sif err = dec.Decode(%s); err != nil {\n", dent, name)
		fmt.Fprintf(f, "%sreturn\n%s}\n", dent+ident, dent)
		return
	}

	// Handle arrays
	if field.Type[0] == '[' {
		// Make sure we have this loop var initialized
		if _, ok := s._declared["_arrLen"]; !ok {
			s._declared["_arrLen"] = "int"
			fmt.Fprintf(f, "%svar _arrLen int\n", dent)
		}
		// temp var for the type
		init, temp := initType(field.Type)
		fmt.Fprintf(f, "%s%s\n", dent, init)
		// Read the len
		s.DecodeField(identCount, Field{"_arrLen", "int", ""}, f)
		// Make the buffer
		fmt.Fprintf(f, "%s%s = make(%s, 0, _arrLen)\n", dent, name, field.Type)
		fmt.Fprintf(f, "%sfor i := 0; i < _arrLen; i++ {\n", dent)

		if err := s.DecodeField(identCount+1, Field{temp, field.Type[2:], ""}, f); err != nil {
			return err
		}
		fmt.Fprintf(f, "%s%s = append(%s, %s)\n", dent+ident, name, name, temp)
		fmt.Fprintln(f, dent+"}")
	}
	return nil
}

/* Each var that is appended to an array needs to be intialized, and have a unique name per type.
This function determines how to handle that properly */
func initType(typ string) (init string, name string) {
	clean_typ := strings.Trim(typ, "[]")
	name = "_" + strings.ToLower(strings.TrimLeft(clean_typ, "*"))
	if typ[0] == '*' {
		init = fmt.Sprintf("var %s = new(%s)", name, clean_typ)
	} else {
		init = fmt.Sprintf("var %s %s", name, clean_typ)
	}
	return
}

func GetFieldType(f ast.Expr) (result string) {
	switch t := f.(type) {
	case *ast.Ident:
		// basic types (e.g. Int)
		result = t.Name
	case *ast.StarExpr:
		// pointer types
		if v, ok := t.X.(*ast.Ident); !ok {
			return
		} else {
			result = "*" + v.Name
		}
	case *ast.ArrayType:
		result = "[]" + GetFieldType(t.Elt)
	case *ast.SelectorExpr:
		result = t.Sel.Name
	default:
		// uncomment below to error and see new types
		// result = f.(*ast.Ident).Name
		return
	}
	return
}

func GetStructFields(obj *ast.Object) *Struct {
	if obj.Decl == nil {
		return nil
	}

	ts, ok := obj.Decl.(*ast.TypeSpec)
	if !ok {
		return nil // not a type definition
	}
	st, ok := ts.Type.(*ast.StructType)
	if !ok {
		return nil // not a struct
	}

	s := &Struct{
		Name:   ts.Name.Name,
		Fields: make([]Field, 0),
	}

	for _, field := range st.Fields.List {
		f := Field{
			Name: field.Names[0].Name,
			Type: GetFieldType(field.Type),
		}
		// Override the type with anything in a struct tag. E.g. enkodo:"int"
		// skip fields that dont have the enkodo tag
		if field.Tag == nil || !strings.Contains(field.Tag.Value, "enkodo") {
			continue
		}
		match := tag.FindStringSubmatch(field.Tag.Value)
		if len(match) > 1 && len(match[1]) > 1 {
			f.OverrideType = match[1]
		}
		if !unicode.IsUpper(rune(f.Name[0])) || (f.Type == "" && f.OverrideType == "") {
			// Only handle exported variables for now
			continue
		}
		s.Fields = append(s.Fields, f)
	}
	if len(s.Fields) > 0 {
		return s
	}
	return nil
}
func objectsInFile(file string) error {
	fset := token.NewFileSet()
	fil, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		log.Fatalf("failed to parse %s: %s", file, err)
	}

	pkg := fil.Name.Name // package name

	structs := make([]*Struct, 0)
	for _, obj := range fil.Scope.Objects {
		if obj.Decl == nil {
			continue
		}

		s := GetStructFields(obj)
		if s == nil {
			continue
		}
		structs = append(structs, s)
	}

	if len(structs) == 0 {
		return nil
	}
	// open the output file
	var out io.Writer
	if len(os.Args) > 2 && os.Args[2] == "-" {
		out = os.Stdout
	} else {
		filename := file[:len(file)-len(filepath.Ext(file))] + "_enkodo.go"
		fmt.Printf("Found %d structs in %s, saving to %s\n", len(structs), file, filename)
		oFile, err := os.Create(filename)
		if err != nil {
			return err
		}
		defer oFile.Close()
		out = oFile
	}

	fmt.Fprint(out, "/* This file is auto-generated by enkodo */\n")
	fmt.Fprintf(out, "package %s\n\nimport \"%s\"\n\n", pkg, packageName)
	for _, st := range structs {
		st.EncodeFunc(out)
		st.DecodeFunc(out)
	}
	return nil
}

func main() {
	opath := os.Args[1]
	files := make([]string, 0, 10)

	filepath.WalkDir(opath, func(path string, d fs.DirEntry, err error) error {
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})

	if len(files) == 0 {
		log.Fatal("No input files given")
	}
	fmt.Println(files)
	for _, file := range files {
		objectsInFile(file)
	}
}
