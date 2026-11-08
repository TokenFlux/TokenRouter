// Package site owns public site settings, localized content, announcements, and pages.
//
// Start here:
//   - public.go: resolves browser settings for public APIs and embedded HTML.
//   - settings_participant.go: prepares site changes for the combined settings transaction.
//   - share_image.go: validates uploaded covers and identifies their public image resources.
//
// File groups:
//   - admin_settings*.go and display_settings.go: administrator projections and runtime display readers.
//   - public*.go and localization.go: public input allowlists, language resolution, and response assembly.
//   - announcement.go, service.go, ports.go, and expiry.go: announcement contracts, publication, and expiration.
//   - pages.go and navigation.go: page visibility and localized navigation configuration.
//   - share_image.go: bounded cover uploads, descriptors, and digest-addressed reads.
package site
