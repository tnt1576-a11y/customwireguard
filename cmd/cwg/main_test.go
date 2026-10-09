package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseProbeSizes(t *testing.T) {
	for _, test := range []struct {
		input string
		want  []int
	}{
		{"", nil},
		{"256, 257,1439,1440", []int{256, 257, 1439, 1440}},
		{"512,512", []int{512, 512}},
	} {
		got, err := parseProbeSizes(test.input)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Fatalf("parse %q: %v, %v", test.input, got, err)
		}
	}
	for _, input := range []string{"255", "1441", "-1", "256,", ",512", "256.5", "NaN", strings.Repeat("256,", 32) + "256"} {
		if _, err := parseProbeSizes(input); err == nil {
			t.Fatalf("accepted malformed/out-of-range sizes %q", input)
		}
	}
}

func TestInterfaceFlagValidation(t *testing.T) {
	for _, args := range [][]string{
		{"server", "-interface-index", "1"},
		{"probe", "-interface-index", "1"},
		{"client", "-interface-index", "-1"},
		{"client", "-interface-index", "16777216"},
	} {
		if err := run(args); err == nil || !strings.Contains(err.Error(), "-interface-index") {
			t.Fatalf("expected flag rejection for %v, got %v", args, err)
		}
	}
}
