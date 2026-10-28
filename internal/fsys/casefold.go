package fsys

// declaredCase is an FS whose answer to [FS.CaseInsensitive] the job has
// given, in place of the one the protocol would guess.
//
// The guess cannot always be right. Neither sftp nor ftp has a way to ask
// whether names that differ only in case are one name, so a server that folds
// case — OpenSSH on Windows — passes for one that does not, and two files that
// differ only in case would be sent onto one name. remote.case_insensitive says
// what goft cannot find out.
type declaredCase struct {
	FS
	insensitive bool
}

// CaseInsensitive implements FS with the declared answer.
func (d *declaredCase) CaseInsensitive() bool { return d.insensitive }

// declaredHostCase is a declaredCase over a directory on this machine, which
// still has to give its paths to post-processing.
type declaredHostCase struct {
	*declaredCase
	host HostPather
}

// HostPath implements HostPather by asking the directory underneath.
func (d *declaredHostCase) HostPath(name string) string { return d.host.HostPath(name) }

// declareCase applies the job's answer about case, if it gave one.
func declareCase(fs FS, insensitive *bool) FS {
	if insensitive == nil {
		return fs
	}
	d := &declaredCase{FS: fs, insensitive: *insensitive}
	if hp, ok := fs.(HostPather); ok {
		return &declaredHostCase{declaredCase: d, host: hp}
	}
	return d
}
