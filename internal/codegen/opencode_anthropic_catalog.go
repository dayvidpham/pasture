package codegen

const openCodeAnthropicProvider OpenCodeProviderID = "anthropic"

// openCodeAnthropicCatalog is the selectable Anthropic model inventory. The
// emitter validates and sorts these records before deriving agent definitions.
var openCodeAnthropicCatalog = []OpenCodeProviderVariant{
	{Provider: openCodeAnthropicProvider, Model: OpenCodeModelID("claude-fable-5"), Slug: OpenCodeVariantSlug("fable-5")},
	{Provider: openCodeAnthropicProvider, Model: OpenCodeModelID("claude-sonnet-5"), Slug: OpenCodeVariantSlug("sonnet-5")},
	{Provider: openCodeAnthropicProvider, Model: OpenCodeModelID("claude-opus-4-8"), Slug: OpenCodeVariantSlug("opus-4-8")},
	{Provider: openCodeAnthropicProvider, Model: OpenCodeModelID("claude-haiku-4-5"), Slug: OpenCodeVariantSlug("haiku-4-5")},
}
