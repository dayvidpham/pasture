package codegen

type openCodeAgentEmitter struct{}

func (openCodeAgentEmitter) Emit(string, string, GenerateOptions) ([]GeneratedFile, error) {
	return nil, nil
}
