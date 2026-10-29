package tuning

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"testing"
)

func TestCaptureDoesNotUseNativeBlockingRead(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "pitch_detector.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Read" {
			t.Error("capture still calls native blocking Read; callback capture must bound waits without locking control methods")
		}
		return true
	})
}

func TestCalculateNoteWithoutStreamReportsAudioReadError(t *testing.T) {
	pd := &PitchDetector{}
	msg := pd.CalculateNote()()
	value := reflect.ValueOf(msg)
	if value.Type().Name() != "AudioReadErrorMsg" {
		t.Fatalf("missing stream returned %T; want AudioReadErrorMsg, not an ordinary reading", msg)
	}
	field := value.FieldByName("Err")
	if !field.IsValid() || field.IsNil() {
		t.Fatal("AudioReadErrorMsg must contain a non-nil Err")
	}
}
