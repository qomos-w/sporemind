package timer

import "reflect"

func isNil(i interface{}) bool {
	if i == nil {
		return true
	}
	v := reflect.ValueOf(i)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

func isString(i interface{}) bool {
	return !isNil(i) && reflect.TypeOf(i).Kind() == reflect.String
}

func isInt(i interface{}) bool {
	return !isNil(i) && reflect.TypeOf(i).Kind() == reflect.Int
}

func isBool(i interface{}) bool {
	return !isNil(i) && reflect.TypeOf(i).Kind() == reflect.Bool
}
