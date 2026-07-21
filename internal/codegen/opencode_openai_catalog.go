package codegen

const (
	openCodeOpenAIProvider OpenCodeProviderID = "openai"

	openCodeOpenAIModelGPT56Sol   OpenCodeModelID = "gpt-5.6-sol"
	openCodeOpenAIModelGPT56Terra OpenCodeModelID = "gpt-5.6-terra"
	openCodeOpenAIModelGPT56Luna  OpenCodeModelID = "gpt-5.6-luna"

	openCodeOpenAISlugGPT56Sol   OpenCodeVariantSlug = "gpt-5-6-sol"
	openCodeOpenAISlugGPT56Terra OpenCodeVariantSlug = "gpt-5-6-terra"
	openCodeOpenAISlugGPT56Luna  OpenCodeVariantSlug = "gpt-5-6-luna"
)

// openCodeOpenAIVariants is the complete set of selectable OpenAI agent
// variants. Registry wiring is kept separate so this provider catalog remains
// independently validatable.
var openCodeOpenAIVariants = []OpenCodeProviderVariant{
	{Provider: openCodeOpenAIProvider, Model: openCodeOpenAIModelGPT56Sol, Slug: openCodeOpenAISlugGPT56Sol},
	{Provider: openCodeOpenAIProvider, Model: openCodeOpenAIModelGPT56Terra, Slug: openCodeOpenAISlugGPT56Terra},
	{Provider: openCodeOpenAIProvider, Model: openCodeOpenAIModelGPT56Luna, Slug: openCodeOpenAISlugGPT56Luna},
}
