package otto

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"unicode/utf8"

	"github.com/robertkrimen/otto/parser"
)

type regExpObject struct {
	regularExpression *regexp.Regexp
	source            string
	flags             string
	global            bool
	ignoreCase        bool
	multiline         bool

	// after is regularExpression preceded by (?s:.), compiled on first use by
	// regExpAfter. It is nil if the pattern does not need the text before the
	// search offset as context.
	after        *regexp.Regexp
	afterChecked bool
}

func (rt *runtime) newRegExpObject(pattern string, flags string) *object {
	o := rt.newObject()
	o.class = classRegExpName

	global := false
	ignoreCase := false
	multiline := false
	re2flags := ""

	// TODO Maybe clean up the panicking here... TypeError, SyntaxError, ?

	for _, chr := range flags {
		switch chr {
		case 'g':
			if global {
				panic(rt.panicSyntaxError("newRegExpObject: %s %s", pattern, flags))
			}
			global = true
		case 'm':
			if multiline {
				panic(rt.panicSyntaxError("newRegExpObject: %s %s", pattern, flags))
			}
			multiline = true
			re2flags += "m"
		case 'i':
			if ignoreCase {
				panic(rt.panicSyntaxError("newRegExpObject: %s %s", pattern, flags))
			}
			ignoreCase = true
			re2flags += "i"
		}
	}

	re2pattern, err := parser.TransformRegExp(pattern)
	if err != nil {
		panic(rt.panicTypeError("Invalid regular expression: %s", err.Error()))
	}
	if len(re2flags) > 0 {
		re2pattern = fmt.Sprintf("(?%s:%s)", re2flags, re2pattern)
	}

	regularExpression, err := regexp.Compile(re2pattern)
	if err != nil {
		panic(rt.panicSyntaxError("Invalid regular expression: %s", err.Error()[22:]))
	}

	o.value = regExpObject{
		regularExpression: regularExpression,
		global:            global,
		ignoreCase:        ignoreCase,
		multiline:         multiline,
		source:            pattern,
		flags:             flags,
	}
	o.defineProperty("global", boolValue(global), 0, false)
	o.defineProperty("ignoreCase", boolValue(ignoreCase), 0, false)
	o.defineProperty("multiline", boolValue(multiline), 0, false)
	o.defineProperty("lastIndex", intValue(0), 0o100, false)
	o.defineProperty("source", stringValue(pattern), 0, false)
	return o
}

func (o *object) regExpValue() regExpObject {
	value, _ := o.value.(regExpObject)
	return value
}

func execRegExp(this *object, target string) (bool, []int) {
	if this.class != classRegExpName {
		panic(this.runtime.panicTypeError("Calling RegExp.exec on a non-RegExp object"))
	}
	lastIndex := this.get("lastIndex").number().int64
	index := lastIndex
	global := this.get("global").bool()
	if !global {
		index = 0
	}

	var result []int
	if 0 > index || index > int64(len(target)) {
	} else {
		result = this.regExpFind(target, int(index))
	}

	if result == nil {
		this.put("lastIndex", intValue(0), true)
		return false, nil
	}

	if global {
		this.put("lastIndex", intValue(result[1]), true)
	}

	return true, result
}

// regExpFind returns the submatch indices in target of the leftmost match
// that starts at or after byte offset index. As in JavaScript, ^, \b and \B
// see the text before index rather than treating index as the start of input.
func (o *object) regExpFind(target string, index int) []int {
	re, start := o.regExpValue().regularExpression, index
	if index > 0 {
		_, w := utf8.DecodeLastRuneInString(target[:index])
		// If index is inside a rune there is no rune to use as context.
		if _, fw := utf8.DecodeRuneInString(target[index-w:]); fw == w {
			if after := o.regExpAfter(); after != nil {
				re, start = after, index-w
			}
		}
	}

	result := re.FindStringSubmatchIndex(target[start:])
	if result == nil {
		return nil
	}
	for i, offset := range result {
		if offset != -1 {
			result[i] += start
		}
	}
	if start != index {
		// Skip the context rune consumed by the (?s:.) prefix.
		_, w := utf8.DecodeRuneInString(target[result[0]:])
		result[0] += w
	}
	return result
}

// regExpAfter returns the regexp's pattern preceded by (?s:.), which matches
// at an offset with the rune before it as context, or nil if the pattern has
// no ^, \b or \B and so does not depend on the text before a match.
func (o *object) regExpAfter() *regexp.Regexp {
	value := o.regExpValue()
	if !value.afterChecked {
		value.afterChecked = true
		pattern := value.regularExpression.String()
		if parsed, err := syntax.Parse(pattern, syntax.Perl); err != nil || regExpNeedsContext(parsed) {
			// If the prefixed pattern somehow fails to compile, fall back to
			// searching without context.
			value.after, _ = regexp.Compile(`(?s:.)(?:` + pattern + `)`)
		}
		o.value = value
	}
	return value.after
}

// regExpNeedsContext reports whether re contains an assertion that depends on
// the text before the current position.
func regExpNeedsContext(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpBeginLine, syntax.OpBeginText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return true
	}
	for _, sub := range re.Sub {
		if regExpNeedsContext(sub) {
			return true
		}
	}
	return false
}

func execResultToArray(rt *runtime, target string, result []int) *object {
	captureCount := len(result) / 2
	valueArray := make([]Value, captureCount)
	for index := range captureCount {
		offset := 2 * index
		if result[offset] != -1 {
			valueArray[index] = stringValue(target[result[offset]:result[offset+1]])
		} else {
			valueArray[index] = Value{}
		}
	}
	matchIndex := result[0]
	if matchIndex != 0 {
		// Find the utf16 index in the string, not the byte index.
		matchIndex = utf16Length(target[:matchIndex])
	}
	match := rt.newArrayOf(valueArray)
	match.defineProperty("input", stringValue(target), 0o111, false)
	match.defineProperty("index", intValue(matchIndex), 0o111, false)
	return match
}
