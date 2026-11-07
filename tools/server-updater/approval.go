package main

import (
	"io"
	"io/fs"
	"os"
)

type trustedApprovalFiles struct{}

func (trustedApprovalFiles) Open(name string) (fs.File, error) {
	if name != "approved.json" {
		return nil, errInvalid
	}
	if err := trustedPath(approvalPath); err != nil {
		return nil, err
	}
	return os.Open(approvalPath)
}

func loadApproval(files fs.FS) (Approval, error) {
	var a Approval
	f, err := files.Open("approved.json")
	if err != nil {
		return a, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return a, err
	}
	if !st.Mode().IsRegular() || st.Size() > 65536 {
		return a, errInvalid
	}
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return a, err
	}
	if len(data) > 65536 {
		return a, errInvalid
	}
	if err := strictJSON(data, &a); err != nil {
		return a, err
	}
	return a, a.validateImages()
}
