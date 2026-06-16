package codegen

type openCodeManifestEmitter struct{}

func (openCodeManifestEmitter) Emit(string, GenerateOptions) ([]GeneratedFile, error) {
	return nil, nil
}
