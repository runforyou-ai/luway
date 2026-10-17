package agentruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/einorun/llm"
)

func TestMediaProjectorDocument(t *testing.T) {
	var seenName string
	projector := &MediaProjector{
		ReadMedia: func(context.Context, einorun.MediaRef) ([]byte, error) {
			return []byte("%PDF"), nil
		},
		ConvertDocument: func(_ context.Context, name, _ string, data []byte) (string, error) {
			seenName = name
			if name == "" || len(data) == 0 {
				t.Fatal("convert args")
			}
			return "# converted", nil
		},
	}
	adaptation, err := projector.Adapt(context.Background(), einorun.MediaRef{Key: "message:1", MIME: "application/pdf", Size: 4}, einorun.AdaptPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if seenName != "document.pdf" {
		t.Fatalf("convert name = %q, want document.pdf for message key", seenName)
	}
	if len(adaptation.Parts) != 1 || adaptation.Parts[0].Kind != "document" || adaptation.Parts[0].Text != "# converted" {
		t.Fatalf("unexpected adaptation %#v", adaptation)
	}
}

func TestDocumentConvertName(t *testing.T) {
	if got := documentConvertName("message:abc", "application/pdf"); got != "document.pdf" {
		t.Fatalf("message key: %q", got)
	}
	if got := documentConvertName("shared:reports/q1.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"); got != "q1.docx" {
		t.Fatalf("shared path: %q", got)
	}
}

func TestProjectableAudioIncludesChannelFormats(t *testing.T) {
	for _, mime := range []string{"audio/ogg", "audio/mpeg", "audio/amr", "audio/mp4", "audio/x-m4a"} {
		modality, ok := projectableModality(mime)
		if !ok || modality != llm.Audio {
			t.Fatalf("%s should be projectable audio", mime)
		}
		if !IsAudioMIME(mime) {
			t.Fatalf("%s should be IsAudioMIME", mime)
		}
	}
}

func TestMediaProjectorOCRRequiresModel(t *testing.T) {
	projector := &MediaProjector{
		MediaURL: func(context.Context, einorun.MediaRef) (string, error) {
			return "https://example.test/a.png", nil
		},
	}
	_, err := projector.Adapt(context.Background(), einorun.MediaRef{Key: "message:1", MIME: "image/png", Size: 10}, einorun.AdaptPolicy{
		Accepted: []llm.Modality{},
	})
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("expected OCR model missing error, got %v", err)
	}
}

func TestMediaProjectorTranscriptionRequiresModel(t *testing.T) {
	projector := &MediaProjector{
		ReadMedia: func(context.Context, einorun.MediaRef) ([]byte, error) {
			return []byte("wav"), nil
		},
	}
	_, err := projector.Adapt(context.Background(), einorun.MediaRef{Key: "message:1", MIME: "audio/ogg", Size: 3}, einorun.AdaptPolicy{})
	if err == nil || !strings.Contains(err.Error(), "audio") {
		t.Fatalf("expected transcription model missing error, got %v", err)
	}
}
