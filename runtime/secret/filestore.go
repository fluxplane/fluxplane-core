package secret

import sharedsecret "github.com/fluxplane/fluxplane-secret"

const DefaultFileStorePath = sharedsecret.DefaultFileStorePath

type StoredSecret = sharedsecret.StoredSecret
type FileStore = sharedsecret.FileStore

func NewFileStore(dir string) FileStore { return sharedsecret.NewFileStore(dir) }
