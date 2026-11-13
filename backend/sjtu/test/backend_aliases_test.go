package test

import backend "github.com/nyte/TboxRclone/backend/sjtu"

type Fs = backend.Fs
type Object = backend.Object
type Options = backend.Options
type AccessRequest = backend.AccessRequest
type MutationSession = backend.MutationSession

const DefaultEncoding = backend.DefaultEncoding
const MaxActiveMutations = backend.MaxActiveMutations

var ErrPathBusy = backend.ErrPathBusy
var NewFs = backend.NewFs
