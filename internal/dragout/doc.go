// Package dragout starts a native OS file drag of sample files out of the
// application window, and reveals files in the system file manager.
//
// The webview cannot produce a drag that DAWs and file managers accept as
// files, so the frontend detects the gesture and the OS drag is started from
// Go while the mouse button is still held.
package dragout
