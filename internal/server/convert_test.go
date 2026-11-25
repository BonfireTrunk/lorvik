package server

import (
	"reflect"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// filled returns a T with every exported field set to a non-zero value, so a
// copy that skips a field shows up as a difference.
func filled[T any](t *testing.T) T {
	t.Helper()
	var v T
	rv := reflect.ValueOf(&v).Elem()
	for i := range rv.NumField() {
		f := rv.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(rv.Type().Field(i).Name)
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int, reflect.Int64:
			f.SetInt(int64(i + 1))
		case reflect.Pointer:
			f.Set(reflect.New(f.Type().Elem()))
		case reflect.Struct:
			if f.Type() == reflect.TypeFor[time.Time]() {
				f.Set(reflect.ValueOf(time.Unix(int64(i+1), 0)))
				continue
			}
			f.FieldByName("ID").SetString("id")
		default:
			t.Fatalf("%s: no fill for kind %s", rv.Type().Field(i).Name, f.Kind())
		}
	}
	return v
}

// sharedFieldsCopied fails for every field of brief that full also has but
// did not receive.
func sharedFieldsCopied(t *testing.T, brief, full any) {
	t.Helper()
	b, f := reflect.ValueOf(brief), reflect.ValueOf(full)
	for i := range b.NumField() {
		name := b.Type().Field(i).Name
		got := f.FieldByName(name)
		if !got.IsValid() {
			t.Errorf("%s has %s but %s does not", b.Type().Name(), name, f.Type().Name())
			continue
		}
		if !reflect.DeepEqual(got.Interface(), b.Field(i).Interface()) {
			t.Errorf("%s.%s not copied from the brief", f.Type().Name(), name)
		}
	}
}

func TestFullShapesCarryEveryBriefField(t *testing.T) {
	t.Parallel()
	t.Run("card", func(t *testing.T) {
		t.Parallel()
		b := filled[apitypes.CardBrief](t)
		sharedFieldsCopied(t, b, cardFromBrief(b))
	})
	t.Run("note", func(t *testing.T) {
		t.Parallel()
		b := filled[apitypes.NoteBrief](t)
		sharedFieldsCopied(t, b, noteFromBrief(b))
	})
}
