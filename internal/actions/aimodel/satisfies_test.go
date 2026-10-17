//go:build server

package aimodel

import (
	"testing"

	"github.com/runforyou-ai/luway/internal/domain"
)

func TestSatisfiesOCRAndTranscription(t *testing.T) {
	text := []domain.AIModelInputModality{domain.AIModelInputModalityText}
	textImage := []domain.AIModelInputModality{domain.AIModelInputModalityText, domain.AIModelInputModalityImage}
	textAudio := []domain.AIModelInputModality{domain.AIModelInputModalityText, domain.AIModelInputModalityAudio}
	if Satisfies(domain.AIModelUsageOCR, domain.AIModelTypeChat, text) {
		t.Fatal("OCR must require image")
	}
	if !Satisfies(domain.AIModelUsageOCR, domain.AIModelTypeChat, textImage) {
		t.Fatal("OCR should accept text+image chat models")
	}
	if Satisfies(domain.AIModelUsageTranscription, domain.AIModelTypeChat, textImage) {
		t.Fatal("transcription must require audio")
	}
	if !Satisfies(domain.AIModelUsageTranscription, domain.AIModelTypeChat, textAudio) {
		t.Fatal("transcription should accept text+audio chat models")
	}
	if !Satisfies(domain.AIModelUsageAgent, domain.AIModelTypeChat, text) {
		t.Fatal("agent still only requires text")
	}
}

func TestValidInputModalitiesIncludeFile(t *testing.T) {
	if !validInputModalities([]domain.AIModelInputModality{domain.AIModelInputModalityText, domain.AIModelInputModalityFile}) {
		t.Fatal("file modality should be valid")
	}
}

func TestModalityJSON(t *testing.T) {
	if got := modalityJSON(domain.AIModelInputModalityImage); got != `["image"]` {
		t.Fatalf("modalityJSON = %q", got)
	}
}
