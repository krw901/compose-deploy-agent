package setup

import (
	"reflect"
	"testing"
)

func TestDetectText(t *testing.T) {
	tests := []struct {
		lcAll, lcCtype, lang string
		wantChinese          bool
	}{
		{lang: "", wantChinese: false},
		{lang: "C", wantChinese: false},
		{lang: "POSIX", wantChinese: false},
		{lang: "zh_CN.GBK", wantChinese: false},
		{lang: "zh_CN.UTF-8", wantChinese: true},
		{lang: "en_US.UTF-8", wantChinese: true},
		{lang: "C.UTF-8", wantChinese: true},
		{lang: "de_DE.utf8@euro", wantChinese: true},
		{lcAll: "C", lang: "zh_CN.UTF-8", wantChinese: false},
		{lcCtype: "zh_CN.UTF-8", lang: "C", wantChinese: true},
	}
	for _, tt := range tests {
		t.Setenv("LC_ALL", tt.lcAll)
		t.Setenv("LC_CTYPE", tt.lcCtype)
		t.Setenv("LANG", tt.lang)
		got := DetectText()
		if isChinese := got.StepBind == chinese.StepBind; isChinese != tt.wantChinese {
			t.Errorf("LC_ALL=%q LC_CTYPE=%q LANG=%q: Chinese = %v, want %v",
				tt.lcAll, tt.lcCtype, tt.lang, isChinese, tt.wantChinese)
		}
	}
}

func TestTextByName(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "zh_CN.UTF-8")
	for name, want := range map[string]Text{"zh": chinese, "en": english, "auto": chinese} {
		got, err := TextByName(name)
		if err != nil {
			t.Fatalf("TextByName(%q): %v", name, err)
		}
		if got.StepBind != want.StepBind {
			t.Errorf("TextByName(%q) returned the wrong language", name)
		}
	}
	if _, err := TextByName("fr"); err == nil {
		t.Fatalf("expected error for an unknown language")
	}
}

// TestTextComplete keeps the two languages in sync: every message must be
// set, and both translations must consume the same number of arguments.
func TestTextComplete(t *testing.T) {
	en, zh := reflect.ValueOf(english), reflect.ValueOf(chinese)
	for i := range en.NumField() {
		name := en.Type().Field(i).Name
		enFormat, zhFormat := en.Field(i).String(), zh.Field(i).String()
		if enFormat == "" || zhFormat == "" {
			t.Errorf("%s is empty in one of the languages", name)
			continue
		}
		if countVerbs(enFormat) != countVerbs(zhFormat) {
			t.Errorf("%s: %q and %q use a different number of arguments", name, enFormat, zhFormat)
		}
	}
}

// countVerbs returns the highest argument a format consumes.
func countVerbs(format string) int {
	used := 0
	next := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			continue
		}
		if i+2 < len(format) && format[i] == '[' {
			n := int(format[i+1] - '0')
			next = n
			i += 2
		} else {
			next++
		}
		used = max(used, next)
	}
	return used
}
