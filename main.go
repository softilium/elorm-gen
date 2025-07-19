package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"go/format"
	"log"
	"os"
	"slices"
	"strings"
	"text/template"

	"github.com/alexflint/go-arg"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

type Index struct {
	Unique  bool
	Columns []string
}

type Context struct {
	PackageName string
	Entities    []*Entity
	Fragments   []*Fragment
	TimeIsUsed  bool //flag to indicate if time package is used (assigned from code)
}

type Fragment struct {
	FragmentName string
	Columns      []*EntityColumn
	Indexes      []*Index
}

type Entity struct {
	Owner      *Context
	ObjectName string
	TableName  string
	Fragments  []string
	Columns    []*EntityColumn
	Indexes    []*Index
}

type EntityColumn struct {
	Name        string
	ColumnType  string
	Len         int
	Precision   int
	Scale       int
	RefTypeName string

	// non-loading
	Owner      *Entity
	IsString   bool
	IsBool     bool
	IsInt      bool
	IsRef      bool
	IsNumeric  bool
	IsDateTime bool
}

//go:embed model.tmpl
var ModelSrc string

//go:embed elorm.schema.json
var SchemaSrc string

func checkErr(err error) {
	if err != nil {
		log.Fatalln(err)
	}
}

var args struct {
	Package      string `arg:"-p,--package"`
	InputJson    string `arg:"positional,required"`
	OutputGolang string `arg:"positional,required"`
}

func main() {

	arg.MustParse(&args)

	sch, err := jsonschema.CompileString("", SchemaSrc)
	checkErr(err)

	data, err := os.ReadFile(args.InputJson)
	checkErr(err)

	// first, load into "any" for properly validation
	var v any
	err = json.Unmarshal(data, &v)
	checkErr(err)

	err = sch.Validate(v)
	checkErr(err)

	// load into struct for further processing
	var ctx Context
	err = json.Unmarshal(data, &ctx)
	checkErr(err)
	ctx.TimeIsUsed = false

	if args.Package != "" {
		ctx.PackageName = args.Package
	}

	if ctx.PackageName == "" {
		ctx.PackageName = "main"
	}

	// set owners, flags
	for _, ent := range ctx.Entities {
		ent.Owner = &ctx
		for _, fragName := range ent.Fragments {
			found := false
			for _, frag := range ctx.Fragments {
				if frag.FragmentName == fragName {
					found = true
					for _, col := range frag.Columns {

						if slices.ContainsFunc(ent.Columns, func(c *EntityColumn) bool { return c.Name == col.Name }) {
							panic(fmt.Sprintf("Column %s already exists in entity %s while adding fragment %s \n", col.Name, ent.ObjectName, fragName))
						}
						colCopy := *col
						colCopy2 := colCopy
						ent.Columns = append(ent.Columns, &colCopy2)
					}

					for _, idx := range frag.Indexes {
						idxCopy := *idx
						idxCopy2 := idxCopy
						ent.Indexes = append(ent.Indexes, &idxCopy2)
					}

					break
				}
			}
			if !found {
				panic(fmt.Sprintf("Unable to find fragment %s for entity %s\n\r", fragName, ent.ObjectName))
			}
		}
		for _, col := range ent.Columns {
			enrichCol(col, ent, &ctx)
		}
	}

	slices.SortFunc(ctx.Entities, func(a, b *Entity) int {
		if a.ObjectName < b.ObjectName {
			return -1
		} else if a.ObjectName > b.ObjectName {
			return 1
		}
		return 0
	})

	modelTmpl, err := template.New("model").Parse(ModelSrc)
	checkErr(err)

	_ = os.Remove(args.OutputGolang)

	f, err := os.OpenFile(args.OutputGolang, os.O_CREATE, os.ModeAppend)
	checkErr(err)
	defer f.Close()

	var buf bytes.Buffer

	err = modelTmpl.ExecuteTemplate(&buf, "dbcontext", ctx)
	checkErr(err)

	p, err := format.Source(buf.Bytes())
	checkErr(err)

	_, err = f.Write(p)
	checkErr(err)
}

func enrichCol(col *EntityColumn, ent *Entity, ctx *Context) {
	col.Owner = ent

	col.IsString = col.ColumnType == "string"
	col.IsRef = strings.HasPrefix(col.ColumnType, "ref.")
	col.IsNumeric = col.ColumnType == "numeric"
	col.IsDateTime = col.ColumnType == "datetime"
	col.IsBool = col.ColumnType == "bool"
	col.IsInt = col.ColumnType == "int"

	if col.IsDateTime {
		ctx.TimeIsUsed = ctx.TimeIsUsed || col.IsDateTime
	}

	if !col.IsString && !col.IsRef && !col.IsNumeric && !col.IsDateTime && !col.IsInt && !col.IsBool {
		panic(fmt.Sprintf("Unable to determine field type (allowed: string, ref.*, numeric, datetime, bool, int). Entity %s, field %s\n\r", ent.ObjectName, col.Name))
	}

	if col.IsRef {
		col.RefTypeName = strings.TrimPrefix(col.ColumnType, "ref.")
		found := false
		for _, ent2 := range ctx.Entities {
			if ent2.ObjectName == col.RefTypeName {
				found = true
				break
			}
		}
		if !found {
			panic(fmt.Sprintf("Unable to find entity %s for ref field %s in entity %s\n\r", col.RefTypeName, col.Name, ent.ObjectName))
		}
	}

	if col.IsString && (col.Len < 1 || col.Len > 512) {
		panic(fmt.Sprintf("Len for string field %s should be in 1.512. Entity name %s\n\r", col.Name, ent.ObjectName))
	}
}
