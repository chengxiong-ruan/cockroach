// Code generated by "stringer -type=ParseMode"; DO NOT EDIT.

package pgdate

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[ParseModeYMD-0]
	_ = x[ParseModeDMY-1]
	_ = x[ParseModeMDY-2]
}

const _ParseMode_name = "ParseModeYMDParseModeDMYParseModeMDY"

var _ParseMode_index = [...]uint8{0, 12, 24, 36}

func (i ParseMode) String() string {
	if i >= ParseMode(len(_ParseMode_index)-1) {
		return "ParseMode(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _ParseMode_name[_ParseMode_index[i]:_ParseMode_index[i+1]]
}
