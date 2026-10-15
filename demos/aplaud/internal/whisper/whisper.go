package whisper

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Transcriber struct {
	WhisperCliPath string
	ModelPath      string
	Language       string
	LibPath        string
	GGMLLibPath    string
	VAD            bool
	VADModelPath   string
	VADThreshold   string
}

type Result struct {
	Text       string
	Segments   []Segment
	Duration   float64
	InputFile  string
	OutputFile string
}

type Segment struct {
	Start float64
	End   float64
	Text  string
}

func NewTranscriber(whisperCliPath, modelPath, language, libPath, ggmlLibPath string, vad bool, vadModelPath, vadThreshold string) *Transcriber {
	return &Transcriber{
		WhisperCliPath: whisperCliPath,
		ModelPath:      modelPath,
		Language:       language,
		LibPath:        libPath,
		GGMLLibPath:    ggmlLibPath,
		VAD:            vad,
		VADModelPath:   vadModelPath,
		VADThreshold:   vadThreshold,
	}
}

func (t *Transcriber) Transcribe(inputPath string) (*Result, error) {
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("input file not found: %s", inputPath)
	}

	ext := strings.ToLower(filepath.Ext(inputPath))
	wavPath := inputPath
	var cleanup bool

	if ext == ".ogg" || ext == ".opus" {
		var err error
		wavPath, cleanup, err = t.convertToWAV(inputPath)
		if err != nil {
			return nil, fmt.Errorf("converting to WAV: %w", err)
		}
		if cleanup {
			defer os.Remove(wavPath)
		}
	}

	outputBase := strings.TrimSuffix(inputPath, filepath.Ext(inputPath))
	txtPath := outputBase + ".txt"

	args := []string{
		"-m", t.ModelPath,
		"-f", wavPath,
		"-l", t.Language,
		"-otxt",
		"-of", outputBase,
		"--no-timestamps",
	}

	if t.VAD && t.VADModelPath != "" {
		args = append(args,
			"--vad",
			"--vad-model", t.VADModelPath,
		)
		if t.VADThreshold != "" {
			args = append(args, "--vad-threshold", t.VADThreshold)
		}
	}

	cmd := exec.Command(t.WhisperCliPath, args...)
	if t.LibPath != "" || t.GGMLLibPath != "" {
		ldPath := t.LibPath
		if t.GGMLLibPath != "" {
			if ldPath != "" {
				ldPath = ldPath + ":" + t.GGMLLibPath
			} else {
				ldPath = t.GGMLLibPath
			}
		}
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+ldPath)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("whisper-cli failed: %w\nOutput: %s", err, string(output))
	}

	text, err := os.ReadFile(txtPath)
	if err != nil {
		return nil, fmt.Errorf("reading transcription output: %w", err)
	}

	result := &Result{
		Text:       strings.TrimSpace(string(text)),
		InputFile:  inputPath,
		OutputFile: txtPath,
	}

	return result, nil
}

func (t *Transcriber) convertToWAV(inputPath string) (string, bool, error) {
	wavPath := strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + ".wav"

	if _, err := exec.LookPath("opusdec"); err == nil {
		cmd := exec.Command("opusdec", inputPath, wavPath, "--rate", "16000")
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", false, fmt.Errorf("opusdec failed: %w: %s", err, string(output))
		}
		return wavPath, true, nil
	}

	if _, err := exec.LookPath("ffmpeg"); err == nil {
		cmd := exec.Command("ffmpeg", "-i", inputPath, "-ar", "16000", "-ac", "1", "-f", "wav", wavPath, "-y")
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", false, fmt.Errorf("ffmpeg failed: %w: %s", err, string(output))
		}
		return wavPath, true, nil
	}

	return "", false, fmt.Errorf("neither opusdec nor ffmpeg found for OGG→WAV conversion")
}

func CheckWhisperAvailable(whisperCliPath string) error {
	if _, err := exec.LookPath(whisperCliPath); err != nil {
		return fmt.Errorf("whisper-cli not found at %s: %w", whisperCliPath, err)
	}
	return nil
}

func CheckConversionToolAvailable() error {
	if _, err := exec.LookPath("opusdec"); err == nil {
		return nil
	}
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		return nil
	}
	return fmt.Errorf("neither opusdec nor ffmpeg found (needed for OGG→WAV conversion)")
}
