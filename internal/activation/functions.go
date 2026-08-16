package activation

import (
	"reflect"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"

	"github.com/phatblat/agentic-review/internal/facts"
)

// functionOptions declares spec §5.2's closed, pure function library:
// touches, touches_only, added_over, files_over, has_class, dep_bumped.
// When f is nil, every binding returns errUnavailable — the check env has
// no facts to evaluate against.
func functionOptions(f *facts.Facts) []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("touches",
			cel.Overload("touches_string", []*cel.Type{cel.StringType}, cel.BoolType,
				cel.UnaryBinding(touchesBinding(f)))),
		cel.Function("touches_only",
			cel.Overload("touches_only_list_string", []*cel.Type{cel.ListType(cel.StringType)}, cel.BoolType,
				cel.UnaryBinding(touchesOnlyBinding(f)))),
		cel.Function("added_over",
			cel.Overload("added_over_int", []*cel.Type{cel.IntType}, cel.BoolType,
				cel.UnaryBinding(addedOverBinding(f)))),
		cel.Function("files_over",
			cel.Overload("files_over_int", []*cel.Type{cel.IntType}, cel.BoolType,
				cel.UnaryBinding(filesOverBinding(f)))),
		cel.Function("has_class",
			cel.Overload("has_class_string", []*cel.Type{cel.StringType}, cel.BoolType,
				cel.UnaryBinding(hasClassBinding(f)))),
		cel.Function("dep_bumped",
			cel.Overload("dep_bumped_string_string", []*cel.Type{cel.StringType, cel.StringType}, cel.BoolType,
				cel.BinaryBinding(depBumpedBinding(f)))),
	}
}

func unavailableUnary(ref.Val) ref.Val           { return types.NewErr(errUnavailable) }
func unavailableBinary(ref.Val, ref.Val) ref.Val { return types.NewErr(errUnavailable) }

func touchesBinding(f *facts.Facts) func(ref.Val) ref.Val {
	if f == nil {
		return unavailableUnary
	}
	return func(arg ref.Val) ref.Val {
		s, ok := arg.(types.String)
		if !ok {
			return types.NewErr("touches: expected a string argument")
		}
		return types.Bool(anyPathMatches(f.Diff.Paths, string(s)))
	}
}

func touchesOnlyBinding(f *facts.Facts) func(ref.Val) ref.Val {
	if f == nil {
		return unavailableUnary
	}
	return func(arg ref.Val) ref.Val {
		native, err := arg.ConvertToNative(reflect.TypeOf([]string{}))
		if err != nil {
			return types.NewErr("touches_only: %v", err)
		}
		globs, ok := native.([]string)
		if !ok {
			return types.NewErr("touches_only: expected a list of strings")
		}
		return types.Bool(allPathsMatchSome(f.Diff.Paths, globs))
	}
}

func addedOverBinding(f *facts.Facts) func(ref.Val) ref.Val {
	if f == nil {
		return unavailableUnary
	}
	return func(arg ref.Val) ref.Val {
		n, ok := arg.(types.Int)
		if !ok {
			return types.NewErr("added_over: expected an int argument")
		}
		return types.Bool(int64(f.Diff.Additions) > int64(n))
	}
}

func filesOverBinding(f *facts.Facts) func(ref.Val) ref.Val {
	if f == nil {
		return unavailableUnary
	}
	return func(arg ref.Val) ref.Val {
		n, ok := arg.(types.Int)
		if !ok {
			return types.NewErr("files_over: expected an int argument")
		}
		return types.Bool(int64(f.Diff.FilesChanged) > int64(n))
	}
}

func hasClassBinding(f *facts.Facts) func(ref.Val) ref.Val {
	if f == nil {
		return unavailableUnary
	}
	return func(arg ref.Val) ref.Val {
		s, ok := arg.(types.String)
		if !ok {
			return types.NewErr("has_class: expected a string argument")
		}
		class := string(s)
		for _, c := range f.Diff.Classes {
			if c == class {
				return types.True
			}
		}
		return types.False
	}
}

func depBumpedBinding(f *facts.Facts) func(ref.Val, ref.Val) ref.Val {
	if f == nil {
		return unavailableBinary
	}
	return func(a, b ref.Val) ref.Val {
		eco, ok1 := a.(types.String)
		level, ok2 := b.(types.String)
		if !ok1 || !ok2 {
			return types.NewErr("dep_bumped: expected two string arguments")
		}
		threshold := bumpRank(string(level))
		if threshold < 0 {
			return types.NewErr("dep_bumped: unknown level %q", string(level))
		}
		for _, dc := range f.Deps.Changed {
			if dc.Ecosystem == string(eco) && bumpRank(dc.Bump) >= threshold {
				return types.True
			}
		}
		return types.False
	}
}

// bumpRank orders semver bump levels from smallest to largest change;
// unrecognised levels (including "other") rank -1.
func bumpRank(level string) int {
	switch level {
	case "prerelease":
		return 0
	case "patch":
		return 1
	case "minor":
		return 2
	case "major":
		return 3
	default:
		return -1
	}
}

func anyPathMatches(paths []string, glob string) bool {
	for _, p := range paths {
		if ok, _ := doublestar.Match(glob, p); ok {
			return true
		}
	}
	return false
}

func pathMatchesAnyGlob(path string, globs []string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(g, path); ok {
			return true
		}
	}
	return false
}

// allPathsMatchSome reports whether every path matches at least one glob.
// An empty path set is conservatively false: "touches_only" asserts
// something about the diff's paths, which is vacuous with no paths.
func allPathsMatchSome(paths []string, globs []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !pathMatchesAnyGlob(p, globs) {
			return false
		}
	}
	return true
}
