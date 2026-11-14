package tui

import (
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
)

// inputCursorMode is the cursor behavior of every text field. It blinks in
// the real UI; tests set it to static so no timer commands are produced.
var inputCursorMode = cursor.CursorBlink

// newTextInput builds a single-line input using the configured cursor mode.
func newTextInput() textinput.Model {
	t := textinput.New()
	t.Cursor.SetMode(inputCursorMode)
	return t
}

// newTextArea builds a multi-line input using the configured cursor mode.
func newTextArea() textarea.Model {
	t := textarea.New()
	t.Cursor.SetMode(inputCursorMode)
	return t
}
