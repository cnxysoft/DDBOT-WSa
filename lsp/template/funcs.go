// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/google/uuid"
	"github.com/huandu/xstrings"
	"github.com/shopspring/decimal"
)

// FuncMap is the type of the map defining the mapping from names to functions.
// Each function must have either a single return value, or two return values of
// which the second has type error. In that case, if the second (error)
// return value evaluates to non-nil during execution, execution terminates and
// Execute returns that error.
//
// Errors returned by Execute wrap the underlying error; call errors.As to
// uncover them.
//
// When template execution invokes a function with an argument list, that list
// must be assignable to the function's parameter types. Functions meant to
// apply to arguments of arbitrary type can use parameters of type interface{} or
// of type reflect.Value. Similarly, functions meant to return a result of arbitrary
// type can return interface{} or reflect.Value.
type FuncMap map[string]interface{}

// builtins returns the FuncMap.
// It is not a global variable so the linker can dead code eliminate
// more when this isn't called. See golang.org/issue/36021.
// TODO: revert this back to a global map once golang.org/issue/2559 is fixed.
func builtins() FuncMap {
	var ins = FuncMap{
		"and":     and,
		"call":    call,
		"index":   index,
		"slice":   slice,
		"len":     length,
		"not":     not,
		"or":      or,
		"print":   fmt.Sprint,
		"printf":  fmt.Sprintf,
		"println": fmt.Sprintln,

		// DDBOT template
		"cut":                cut,
		"prefix":             prefix,
		"reply":              reply,
		"pic":                pic,
		"roll":               roll,
		"choose":             choose,
		"at":                 at,
		"icon":               icon,
		"member_info":        memberInfo,
		"member_list":        memberList,
		"poke":               poke,
		"bot_uin":            botUin,
		"addScore":           addScore,
		"subScore":           subScore,
		"setScore":           setScore,
		"getScore":           getScore,
		"delAcct":            delAcct,
		"isAdmin":            isAdmin,
		"getIListJson":       getIListJson,
		"outputIList":        outputIList,
		"jsonToDictOrArray":  jsonToDictOrArray,
		"video":              video,
		"record":             record,
		"file":               file,
		"remoteDownloadFile": remoteDownloadFile,
		"getMsg":             getMsg,
		"getFileUrl":         getFileUrl,

		// DDBOT common
		"hour":          hour,
		"minute":        minute,
		"second":        second,
		"month":         month,
		"year":          year,
		"day":           day,
		"yearday":       yearday,
		"weekday":       weekday,
		"getTimeStamp":  getTimeStamp,
		"getTime":       getTime,
		"getUnixTime":   getUnixTime,
		"cooldown":      cooldown,
		"openFile":      openFile,
		"readLine":      readLine,
		"findReadLine":  findReadLine,
		"findWriteLine": findWriteLine,
		"writeLine":     writeLine,
		"updateFile":    updateFile,
		"writeFile":     writeFile,
		"abort":         abort,
		"fin":           fin,
		"uriEncode":     uriEncode,
		"uriDecode":     uriDecode,
		"loop":          loop,
		"lsDir":         lsDir,
		"getEleType":    getEleType,

		// cast
		"float64": toFloat64,
		"int":     toInt,
		"int64":   toInt64,

		// math
		"add": func(i ...interface{}) int64 {
			var a int64 = 0
			for _, b := range i {
				a += toInt64(b)
			}
			return a
		},
		"sub": func(a, b interface{}) int64 { return toInt64(a) - toInt64(b) },
		"div": func(a, b interface{}) int64 { return toInt64(a) / toInt64(b) },
		"mod": func(a, b interface{}) int64 { return toInt64(a) % toInt64(b) },
		"mul": func(a interface{}, v ...interface{}) int64 {
			val := toInt64(a)
			for _, b := range v {
				val = val * toInt64(b)
			}
			return val
		},
		"addf": func(i ...interface{}) float64 {
			a := interface{}(float64(0))
			return execDecimalOp(a, i, func(d1, d2 decimal.Decimal) decimal.Decimal { return d1.Add(d2) })
		},
		"subf": func(a interface{}, v ...interface{}) float64 {
			return execDecimalOp(a, v, func(d1, d2 decimal.Decimal) decimal.Decimal { return d1.Sub(d2) })
		},
		"divf": func(a interface{}, v ...interface{}) float64 {
			return execDecimalOp(a, v, func(d1, d2 decimal.Decimal) decimal.Decimal { return d1.Div(d2) })
		},
		"modf": func(a interface{}, v ...interface{}) float64 {
			return execDecimalOp(a, v, func(d1, d2 decimal.Decimal) decimal.Decimal { return d1.Mod(d2) })
		},
		"mulf": func(a interface{}, v ...interface{}) float64 {
			return execDecimalOp(a, v, func(d1, d2 decimal.Decimal) decimal.Decimal { return d1.Mul(d2) })
		},

		// max min
		"max":  max,
		"maxf": maxf,
		"min":  min,
		"minf": minf,

		// crypto
		"base64encode": base64encode,
		"base64decode": base64decode,
		"md5sum":       md5sum,
		"sha1sum":      sha1sum,
		"sha256sum":    sha256sum,
		"adler32sum":   adler32sum,
		"uuid":         func() string { return uuid.New().String() },

		// string
		"toString":   strval,
		"trim":       strings.TrimSpace,
		"trimAll":    func(a, b string) string { return strings.Trim(b, a) },
		"trimSuffix": func(a, b string) string { return strings.TrimSuffix(b, a) },
		"trimPrefix": func(a, b string) string { return strings.TrimPrefix(b, a) },
		"contains":   func(substr string, str string) bool { return strings.Contains(str, substr) },
		"hasPrefix":  func(substr string, str string) bool { return strings.HasPrefix(str, substr) },
		"hasSuffix":  func(substr string, str string) bool { return strings.HasSuffix(str, substr) },
		"split":      func(sep, orig string) []string { return strings.Split(orig, sep) },
		"join":       join,
		"trunc":      trunc,
		"reTrunc":    reTrunc,
		"replace":    strings.Replace,
		"replaceAll": func(old, new, str string) string { return strings.ReplaceAll(str, old, new) },
		"find":       strings.Index,
		"findLast":   strings.LastIndex,
		"count":      strings.Count,

		"snakecase": xstrings.ToSnakeCase,
		"camelcase": xstrings.ToCamelCase,
		"kebabcase": xstrings.ToKebabCase,
		"upper":     strings.ToUpper,
		"lower":     strings.ToLower,
		"title":     strings.Title,

		// defaults
		"empty":    empty,
		"nonEmpty": nonEmpty,
		"coalesce": coalesce,
		"ternary":  ternary,
		"all":      all,
		"any":      any,

		// dict
		"dict":               dict,
		"get":                get,
		"set":                set,
		"unset":              unset,
		"hasKey":             hasKey,
		"pluck":              pluck,
		"keys":               keys,
		"pick":               pick,
		"omit":               omit,
		"merge":              merge,
		"mergeOverwrite":     mergeOverwrite,
		"mustMerge":          mustMerge,
		"mustMergeOverwrite": mustMergeOverwrite,
		"values":             values,

		// list
		"list":        list,
		"append":      push,
		"prepend":     prepend,
		"concat":      concat,
		"delStrSlice": delStrSlice,

		// http
		"httpGet":      httpGet,
		"httpPostJson": httpPostJson,
		"httpPostForm": httpPostForm,
		"downloadFile": downloadFile,

		// json
		"toGJson": toGJson,
		"toJson":  toJson,

		// Comparisons
		"eq": eq, // ==
		"ge": ge, // >=
		"gt": gt, // >
		"le": le, // <=
		"lt": lt, // <
		"ne": ne, // !=
	}
	for name := range funcsExt {
		if _, found := ins[name]; found {
			panic(fmt.Sprintf("name %v is already exists", name))
		}
	}
	for name, fn := range funcsExt {
		ins[name] = fn
	}
	return ins
}

var builtinFuncsOnce struct {
	sync.Once
	v map[string]reflect.Value
}

// builtinFuncsOnce lazily computes & caches the builtinFuncs map.
// TODO: revert this back to a global map once golang.org/issue/2559 is fixed.
func builtinFuncs() map[string]reflect.Value {
	builtinFuncsOnce.Do(func() {
		builtinFuncsOnce.v = createValueFuncs(builtins())
	})
	return builtinFuncsOnce.v
}

// createValueFuncs turns a FuncMap into a map[string]reflect.Value
func createValueFuncs(funcMap FuncMap) map[string]reflect.Value {
	m := make(map[string]reflect.Value)
	addValueFuncs(m, funcMap)
	return m
}

func checkValueFuncs(name string, fn interface{}) {
	if !goodName(name) {
		panic(fmt.Errorf("function name %q is not a valid identifier", name))
	}
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func {
		panic("value for " + name + " not a function")
	}
	if !goodFunc(v.Type()) {
		panic(fmt.Errorf("can't install method/function %q with %d results", name, v.Type().NumOut()))
	}
}

// addValueFuncs adds to values the functions in funcs, converting them to reflect.Values.
func addValueFuncs(out map[string]reflect.Value, in FuncMap) {
	for name, fn := range in {
		checkValueFuncs(name, fn)
		out[name] = reflect.ValueOf(fn)
	}
}

// addFuncs adds to values the functions in funcs. It does no checking of the input -
// call addValueFuncs first.
func addFuncs(out, in FuncMap) {
	for name, fn := range in {
		out[name] = fn
	}
}

// goodFunc reports whether the function or method has the right result signature.
func goodFunc(typ reflect.Type) bool {
	// We allow functions with 1 result or 2 results where the second is an error.
	switch {
	case typ.NumOut() == 1:
		return true
	case typ.NumOut() == 2 && typ.Out(1) == errorType:
		return true
	}
	return false
}

// goodName reports whether the function name is a valid identifier.
func goodName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_':
		case i == 0 && !unicode.IsLetter(r):
			return false
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			return false
		}
	}
	return true
}

// findFunction looks for a function in the template, and global map.
func findFunction(name string, tmpl *Template) (reflect.Value, bool) {
	if tmpl != nil && tmpl.common != nil {
		tmpl.muFuncs.RLock()
		defer tmpl.muFuncs.RUnlock()
		if fn := tmpl.execFuncs[name]; fn.IsValid() {
			return fn, true
		}
	}
	if fn := builtinFuncs()[name]; fn.IsValid() {
		return fn, true
	}
	return reflect.Value{}, false
}

// prepareArg checks if value can be used as an argument of type argType, and
// converts an invalid value to appropriate zero if possible.
func prepareArg(value reflect.Value, argType reflect.Type) (reflect.Value, error) {
	if !value.IsValid() {
		if !canBeNil(argType) {
			return reflect.Value{}, fmt.Errorf("value is nil; should be of type %s", argType)
		}
		value = reflect.Zero(argType)
	}
	if value.Type().AssignableTo(argType) {
		return value, nil
	}
	if intLike(value.Kind()) && intLike(argType.Kind()) && value.Type().ConvertibleTo(argType) {
		value = value.Convert(argType)
		return value, nil
	}
	return reflect.Value{}, fmt.Errorf("value has type %s; should be %s", value.Type(), argType)
}

func intLike(typ reflect.Kind) bool {
	switch typ {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	}
	return false
}

// indexArg checks if a reflect.Value can be used as an index, and converts it to int if possible.
func indexArg(index reflect.Value, cap int) (int, error) {
	var x int64
	switch index.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		x = index.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		x = int64(index.Uint())
	case reflect.Invalid:
		return 0, fmt.Errorf("cannot index slice/array with nil")
	default:
		return 0, fmt.Errorf("cannot index slice/array with type %s", index.Type())
	}
	if x < 0 || int(x) < 0 || int(x) > cap {
		return 0, fmt.Errorf("index out of range: %d", x)
	}
	return int(x), nil
}

// Indexing.

// index returns the result of indexing its first argument by the following
// arguments. Thus "index x 1 2 3" is, in Go syntax, x[1][2][3]. Each
// indexed item must be a map, slice, or array.
func index(item reflect.Value, indexes ...reflect.Value) (reflect.Value, error) {
	item = indirectInterface(item)
	if !item.IsValid() {
		return reflect.Value{}, fmt.Errorf("index of untyped nil")
	}
	for _, index := range indexes {
		index = indirectInterface(index)
		var isNil bool
		if item, isNil = indirect(item); isNil {
			return reflect.Value{}, fmt.Errorf("index of nil pointer")
		}
		switch item.Kind() {
		case reflect.Array, reflect.Slice, reflect.String:
			x, err := indexArg(index, item.Len())
			if err != nil {
				return reflect.Value{}, err
			}
			item = item.Index(x)
		case reflect.Map:
			index, err := prepareArg(index, item.Type().Key())
			if err != nil {
				return reflect.Value{}, err
			}
			if x := item.MapIndex(index); x.IsValid() {
				item = x
			} else {
				item = reflect.Zero(item.Type().Elem())
			}
		case reflect.Invalid:
			// the loop holds invariant: item.IsValid()
			panic("unreachable")
		default:
			return reflect.Value{}, fmt.Errorf("can't index item of type %s", item.Type())
		}
	}
	return item, nil
}

// Slicing.

// slice returns the result of slicing its first argument by the remaining
// arguments. Thus "slice x 1 2" is, in Go syntax, x[1:2], while "slice x"
// is x[:], "slice x 1" is x[1:], and "slice x 1 2 3" is x[1:2:3]. The first
// argument must be a string, slice, or array.
func slice(item reflect.Value, indexes ...reflect.Value) (reflect.Value, error) {
	item = indirectInterface(item)
	if !item.IsValid() {
		return reflect.Value{}, fmt.Errorf("slice of untyped nil")
	}
	if len(indexes) > 3 {
		return reflect.Value{}, fmt.Errorf("too many slice indexes: %d", len(indexes))
	}
	var cap int
	switch item.Kind() {
	case reflect.String:
		if len(indexes) == 3 {
			return reflect.Value{}, fmt.Errorf("cannot 3-index slice a string")
		}
		cap = item.Len()
	case reflect.Array, reflect.Slice:
		cap = item.Cap()
	default:
		return reflect.Value{}, fmt.Errorf("can't slice item of type %s", item.Type())
	}
	// set default values for cases item[:], item[i:].
	idx := [3]int{0, item.Len()}
	for i, index := range indexes {
		x, err := indexArg(index, cap)
		if err != nil {
			return reflect.Value{}, err
		}
		idx[i] = x
	}
	// given item[i:j], make sure i <= j.
	if idx[0] > idx[1] {
		return reflect.Value{}, fmt.Errorf("invalid slice index: %d > %d", idx[0], idx[1])
	}
	if len(indexes) < 3 {
		return item.Slice(idx[0], idx[1]), nil
	}
	// given item[i:j:k], make sure i <= j <= k.
	if idx[1] > idx[2] {
		return reflect.Value{}, fmt.Errorf("invalid slice index: %d > %d", idx[1], idx[2])
	}
	return item.Slice3(idx[0], idx[1], idx[2]), nil
}

// Length

// length returns the length of the item, with an error if it has no defined length.
func length(item reflect.Value) (int, error) {
	item, isNil := indirect(item)
	if isNil {
		return 0, fmt.Errorf("len of nil pointer")
	}
	switch item.Kind() {
	case reflect.Array, reflect.Chan, reflect.Map, reflect.Slice, reflect.String:
		return item.Len(), nil
	}
	return 0, fmt.Errorf("len of type %s", item.Type())
}

// Function invocation

// call returns the result of evaluating the first argument as a function.
// The function must return 1 result, or 2 results, the second of which is an error.
func call(fn reflect.Value, args ...reflect.Value) (reflect.Value, error) {
	fn = indirectInterface(fn)
	if !fn.IsValid() {
		return reflect.Value{}, fmt.Errorf("call of nil")
	}
	typ := fn.Type()
	if typ.Kind() != reflect.Func {
		return reflect.Value{}, fmt.Errorf("non-function of type %s", typ)
	}
	if !goodFunc(typ) {
		return reflect.Value{}, fmt.Errorf("function called with %d args; should be 1 or 2", typ.NumOut())
	}
	numIn := typ.NumIn()
	var dddType reflect.Type
	if typ.IsVariadic() {
		if len(args) < numIn-1 {
			return reflect.Value{}, fmt.Errorf("wrong number of args: got %d want at least %d", len(args), numIn-1)
		}
		dddType = typ.In(numIn - 1).Elem()
	} else {
		if len(args) != numIn {
			return reflect.Value{}, fmt.Errorf("wrong number of args: got %d want %d", len(args), numIn)
		}
	}
	argv := make([]reflect.Value, len(args))
	for i, arg := range args {
		arg = indirectInterface(arg)
		// Compute the expected type. Clumsy because of variadics.
		argType := dddType
		if !typ.IsVariadic() || i < numIn-1 {
			argType = typ.In(i)
		}

		var err error
		if argv[i], err = prepareArg(arg, argType); err != nil {
			return reflect.Value{}, fmt.Errorf("arg %d: %w", i, err)
		}
	}
	return safeCall(fn, argv)
}

// safeCall runs fun.Call(args), and returns the resulting value and error, if
// any. If the call panics, the panic value is returned as an error.
func safeCall(fun reflect.Value, args []reflect.Value) (val reflect.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", r)
			}
		}
	}()
	ret := fun.Call(args)
	if len(ret) == 2 && !ret[1].IsNil() {
		return ret[0], ret[1].Interface().(error)
	}
	return ret[0], nil
}

// Boolean logic.

func truth(arg reflect.Value) bool {
	t, _ := isTrue(indirectInterface(arg))
	return t
}

// and computes the Boolean AND of its arguments, returning
// the first false argument it encounters, or the last argument.
func and(arg0 reflect.Value, args ...reflect.Value) reflect.Value {
	if !truth(arg0) {
		return arg0
	}
	for i := range args {
		arg0 = args[i]
		if !truth(arg0) {
			break
		}
	}
	return arg0
}

// or computes the Boolean OR of its arguments, returning
// the first true argument it encounters, or the last argument.
func or(arg0 reflect.Value, args ...reflect.Value) reflect.Value {
	if truth(arg0) {
		return arg0
	}
	for i := range args {
		arg0 = args[i]
		if truth(arg0) {
			break
		}
	}
	return arg0
}

// not returns the Boolean negation of its argument.
func not(arg reflect.Value) bool {
	return !truth(arg)
}

// Comparison.

// TODO: Perhaps allow comparison between signed and unsigned integers.

var (
	errBadComparisonType = errors.New("invalid type for comparison")
	errBadComparison     = errors.New("incompatible types for comparison")
	errNoComparison      = errors.New("missing argument for comparison")
)

type kind int

const (
	invalidKind kind = iota
	boolKind
	complexKind
	intKind
	floatKind
	stringKind
	uintKind
)

func basicKind(v reflect.Value) (kind, error) {
	switch v.Kind() {
	case reflect.Bool:
		return boolKind, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return intKind, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return uintKind, nil
	case reflect.Float32, reflect.Float64:
		return floatKind, nil
	case reflect.Complex64, reflect.Complex128:
		return complexKind, nil
	case reflect.String:
		return stringKind, nil
	}
	return invalidKind, errBadComparisonType
}

func tryStringNumberCmp(arg1 reflect.Value, arg2 reflect.Value, order int) (bool, bool) {
	k1, _ := basicKind(arg1)
	k2, _ := basicKind(arg2)
	var err error
	var result = false
	switch {
	case k1 == stringKind:
		if k2 == intKind {
			var a1 int64
			a1, err = strconv.ParseInt(arg1.String(), 10, 64)
			if err != nil {
				return false, false
			}
			if order == -1 {
				result = a1 < arg2.Int()
			} else if order == 0 {
				result = a1 == arg2.Int()
			} else {
				result = a1 > arg2.Int()
			}
		} else if k2 == uintKind {
			var a1 uint64
			a1, err = strconv.ParseUint(arg1.String(), 10, 64)
			if err != nil {
				return false, false
			}
			if order == -1 {
				result = a1 < arg2.Uint()
			} else if order == 0 {
				result = a1 == arg2.Uint()
			} else {
				result = a1 > arg2.Uint()
			}
		} else {
			return false, false
		}
	case k2 == stringKind:
		if k1 == intKind {
			var a2 int64
			a2, err = strconv.ParseInt(arg2.String(), 10, 64)
			if err != nil {
				return false, false
			}
			if order == -1 {
				result = arg1.Int() < a2
			} else if order == 0 {
				result = arg1.Int() == a2
			} else {
				result = arg1.Int() > a2
			}
		} else if k1 == uintKind {
			var a2 uint64
			a2, err = strconv.ParseUint(arg2.String(), 10, 64)
			if err != nil {
				return false, false
			}
			if order == -1 {
				result = arg1.Uint() < a2
			} else if order == 0 {
				result = arg1.Uint() == a2
			} else {
				result = arg1.Uint() > a2
			}
		} else {
			return false, false
		}
	default:
		return false, false
	}
	return result, true
}

// eq evaluates the comparison a == b || a == c || ...
func eq(arg1 reflect.Value, arg2 ...reflect.Value) (bool, error) {
	arg1 = indirectInterface(arg1)
	if arg1 != zero {
		if t1 := arg1.Type(); !t1.Comparable() {
			return false, fmt.Errorf("uncomparable type %s: %v", t1, arg1)
		}
	}
	if len(arg2) == 0 {
		return false, errNoComparison
	}
	k1, _ := basicKind(arg1)
	for _, arg := range arg2 {
		arg = indirectInterface(arg)
		k2, _ := basicKind(arg)
		if result, success := tryStringNumberCmp(arg1, arg, 0); success {
			return result, nil
		}
		truth := false
		if k1 != k2 {
			// Special case: Can compare integer values regardless of type's sign.
			switch {
			case k1 == intKind && k2 == uintKind:
				truth = arg1.Int() >= 0 && uint64(arg1.Int()) == arg.Uint()
			case k1 == uintKind && k2 == intKind:
				truth = arg.Int() >= 0 && arg1.Uint() == uint64(arg.Int())
			default:
				if arg1 != zero && arg != zero {
					return false, errBadComparison
				}
			}
		} else {
			switch k1 {
			case boolKind:
				truth = arg1.Bool() == arg.Bool()
			case complexKind:
				truth = arg1.Complex() == arg.Complex()
			case floatKind:
				truth = arg1.Float() == arg.Float()
			case intKind:
				truth = arg1.Int() == arg.Int()
			case stringKind:
				truth = arg1.String() == arg.String()
			case uintKind:
				truth = arg1.Uint() == arg.Uint()
			default:
				if arg == zero || arg1 == zero {
					truth = arg1 == arg
				} else {
					if t2 := arg.Type(); !t2.Comparable() {
						return false, fmt.Errorf("uncomparable type %s: %v", t2, arg)
					}
					truth = arg1.Interface() == arg.Interface()
				}
			}
		}
		if truth {
			return true, nil
		}
	}
	return false, nil
}

// ne evaluates the comparison a != b.
func ne(arg1, arg2 reflect.Value) (bool, error) {
	// != is the inverse of ==.
	equal, err := eq(arg1, arg2)
	return !equal, err
}

// lt evaluates the comparison a < b.
func lt(arg1, arg2 reflect.Value) (bool, error) {
	arg1 = indirectInterface(arg1)
	k1, err := basicKind(arg1)
	if err != nil {
		return false, err
	}
	arg2 = indirectInterface(arg2)
	k2, err := basicKind(arg2)
	if err != nil {
		return false, err
	}
	if result, success := tryStringNumberCmp(arg1, arg2, -1); success {
		return result, nil
	}
	truth := false
	if k1 != k2 {
		// Special case: Can compare integer values regardless of type's sign.
		switch {
		case k1 == intKind && k2 == uintKind:
			truth = arg1.Int() < 0 || uint64(arg1.Int()) < arg2.Uint()
		case k1 == uintKind && k2 == intKind:
			truth = arg2.Int() >= 0 && arg1.Uint() < uint64(arg2.Int())
		default:
			return false, errBadComparison
		}
	} else {
		switch k1 {
		case boolKind, complexKind:
			return false, errBadComparisonType
		case floatKind:
			truth = arg1.Float() < arg2.Float()
		case intKind:
			truth = arg1.Int() < arg2.Int()
		case stringKind:
			truth = arg1.String() < arg2.String()
		case uintKind:
			truth = arg1.Uint() < arg2.Uint()
		default:
			panic("invalid kind")
		}
	}
	return truth, nil
}

// le evaluates the comparison <= b.
func le(arg1, arg2 reflect.Value) (bool, error) {
	// <= is < or ==.
	lessThan, err := lt(arg1, arg2)
	if lessThan || err != nil {
		return lessThan, err
	}
	return eq(arg1, arg2)
}

// gt evaluates the comparison a > b.
func gt(arg1, arg2 reflect.Value) (bool, error) {
	// > is the inverse of <=.
	lessOrEqual, err := le(arg1, arg2)
	if err != nil {
		return false, err
	}
	return !lessOrEqual, nil
}

// ge evaluates the comparison a >= b.
func ge(arg1, arg2 reflect.Value) (bool, error) {
	// >= is the inverse of <.
	lessThan, err := lt(arg1, arg2)
	if err != nil {
		return false, err
	}
	return !lessThan, nil
}

// evalArgs formats the list of arguments into a string. It is therefore equivalent to
//
//	fmt.Sprint(args...)
//
// except that each argument is indirected (if a pointer), as required,
// using the same rules as the default string evaluation during template
// execution.
func evalArgs(args []interface{}) string {
	ok := false
	var s string
	// Fast path for simple common case.
	if len(args) == 1 {
		s, ok = args[0].(string)
	}
	if !ok {
		for i, arg := range args {
			a, ok := printableValue(reflect.ValueOf(arg))
			if ok {
				args[i] = a
			} // else let fmt do its thing
		}
		s = fmt.Sprint(args...)
	}
	return s
}
