// Package run: a no-op file whose only purpose is to touch a path the image workflow watches, so the
// branch image is rebuilt and the walkthrough workflow (which triggers on that build) runs. Deleted
// immediately in the next commit - it carries no code.
package run
