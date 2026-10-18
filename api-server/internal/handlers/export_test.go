package handlers

// WaitForPasswordResetWork blocks until every background forgot-password task
// started so far has finished, so tests can assert on what it did (or did not)
// send. It is compiled into test binaries only.
func (h *UserAuthHandler) WaitForPasswordResetWork() { h.resetWG.Wait() }
