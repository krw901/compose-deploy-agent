package config

// ImageRefMode is how a service's deployed version is referenced in compose.
type ImageRefMode string

const (
	// ImageRefTag references the image as repo:<tag> (the default).
	ImageRefTag ImageRefMode = "tag"
	// ImageRefDigest references the image as repo@sha256:<hex>; plain tags
	// are rejected so a re-pointed tag can never change what gets deployed.
	ImageRefDigest ImageRefMode = "digest"
)

// UsesDigest reports whether the service only accepts sha256 digests.
func (s Service) UsesDigest() bool {
	return s.ImageRef == ImageRefDigest
}

// ImageReference returns the full image reference compose resolves for
// ref: "image@ref" in digest mode, "image:ref" otherwise.
func (s Service) ImageReference(ref string) string {
	if s.UsesDigest() {
		return s.Image + "@" + ref
	}
	return s.Image + ":" + ref
}
