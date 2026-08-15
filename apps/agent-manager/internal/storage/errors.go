package storage

import "errors"

var ErrNotFound = errors.New("storage: not found")
var ErrSessionClosed = errors.New("storage: session is closed")
var ErrRunActive = errors.New("storage: session has an active run")
var ErrConflict = errors.New("storage: conflict")
