#ifndef SSSS_DRAGOUT_CGO_H
#define SSSS_DRAGOUT_CGO_H

#include <stdint.h>

// Results passed to dragoutDone.
enum {
    DRAGOUT_DROPPED = 1,
    DRAGOUT_CANCELLED = 0,
    DRAGOUT_ERR_NO_WINDOW = -1,
    DRAGOUT_ERR_BUTTON_RELEASED = -2,
    DRAGOUT_ERR_START_FAILED = -3,
};

// dragout_start begins a file drag from the native window on the UI thread
// and returns immediately; it may be called from any thread. Everything it
// needs from paths and label is copied before it returns. dragoutDone(handle,
// result) is called exactly once, when the drag has ended or could not start.
// begin() implementations return DRAGOUT_DROPPED to mean "started".
void dragout_start(void *window, char **paths, int n, const char *label, uintptr_t handle);

#endif
