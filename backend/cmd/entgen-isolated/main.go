package main

import (
	"flag"
	"fmt"
	"os"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
	"entgo.io/ent/schema/field"
)

func main() {
	target := flag.String("target", "./entgen-output", "staging directory for generated Ent code")
	flag.Parse()

	cfg := &gen.Config{
		Target:  *target,
		Package: "github.com/Wei-Shaw/sub2api/ent",
		IDType:  &field.TypeInfo{Type: field.TypeInt64},
		Features: []gen.Feature{
			gen.FeatureUpsert,
			gen.FeatureIntercept,
			gen.FeatureExecQuery,
			gen.FeatureLock,
		},
	}
	if err := entc.Generate("./ent/schema", cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
