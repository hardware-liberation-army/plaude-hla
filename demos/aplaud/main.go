package main

import (
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"aplaud/internal/ble"
	"aplaud/internal/config"
	"aplaud/internal/crypto"
	"aplaud/internal/filesync"
	"aplaud/internal/handshake"
	"aplaud/internal/llm"
	"aplaud/internal/memory"
	"aplaud/internal/protocol"
	"aplaud/internal/tui"
	"aplaud/internal/whisper"
)

var cfgPath string

func main() {
	rootCmd := &cobra.Command{
		Use:   "aplaud",
		Short: "Plaud BLE to Transcription Pipeline",
		Long:  "A CLI that scans for a Plaud Note Pro device, downloads recordings via BLE, decrypts them, transcribes with whisper.cpp, and deletes them from the device.",
	}

	rootCmd.PersistentFlags().StringVar(&cfgPath, "config", "", "Config file path (default: ~/.config/aplaud/config.yaml)")

	rootCmd.AddCommand(scanCmd())
	rootCmd.AddCommand(downloadCmd())
	rootCmd.AddCommand(decryptCmd())
	rootCmd.AddCommand(transcribeCmd())
	rootCmd.AddCommand(oneshotCmd())
	rootCmd.AddCommand(doctorCmd())
	rootCmd.AddCommand(tuiCmd())
	rootCmd.AddCommand(summarizeCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func loadConfig() *config.Config {
	path := cfgPath
	if path == "" {
		path = config.ConfigPath()
	}

	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config from %s: %v\n", path, err)
		fmt.Fprintf(os.Stderr, "Create a config file at ~/.config/aplaud/config.yaml (see config.example.yaml)\n")
		os.Exit(1)
	}
	return cfg
}

func scanCmd() *cobra.Command {
	var duration float64
	var showAll bool

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan for Plaud BLE devices",
		Run: func(cmd *cobra.Command, args []string) {
			if showAll {
				devices, err := ble.ScanAll(duration)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Scan error: %v\n", err)
					os.Exit(1)
				}
				fmt.Printf("\nFound %d BLE device(s):\n\n", len(devices))
				fmt.Printf("%-20s %5s  %-30s\n", "Address", "RSSI", "Name")
				fmt.Println(strings.Repeat("-", 60))
				for _, d := range devices {
					name := d.Name
					if name == "" {
						name = "(unknown)"
					}
					plaud := ""
					if ble.FindPlaudByName(name) {
						plaud = " <<< PLAUD"
					}
					fmt.Printf("%-20s %5d  %-30s%s\n", d.Address.String(), d.RSSI, name, plaud)
				}
			} else {
				devices, err := ble.Scan(duration)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Scan error: %v\n", err)
					os.Exit(1)
				}
				if len(devices) == 0 {
					fmt.Println("No Plaud devices found.")
					return
				}
				fmt.Printf("\nFound %d Plaud device(s):\n\n", len(devices))
				for _, d := range devices {
					name := d.Name
					if name == "" {
						name = "(unknown)"
					}
					fmt.Printf("  %s  %s\n", d.Address.String(), name)
				}
			}
		},
	}

	cmd.Flags().Float64VarP(&duration, "duration", "d", 10.0, "Scan duration in seconds")
	cmd.Flags().BoolVar(&showAll, "all", false, "Show all BLE devices")

	return cmd
}

func downloadCmd() *cobra.Command {
	var address string
	var allFiles bool
	var sessionID int
	var outputDir string

	cmd := &cobra.Command{
		Use:   "download",
		Short: "Connect, download all files, decrypt to OGG",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := loadConfig()
			transport := ble.NewTransport()

			_, err := findAndConnect(transport, cfg, address)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Connection failed: %v\n", err)
				os.Exit(1)
			}
			defer transport.Disconnect()

			hs := handshake.NewHandshaker(transport, cfg)
			result, err := hs.DoHandshake()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Handshake failed: %v\n", err)
				os.Exit(1)
			}

			session := result.Session

			fs := filesync.NewFileSyncManager(
				func(data []byte) ([]byte, error) {
					return session.DecryptRecv(data)
				},
				func(data []byte) error {
					return hs.EncryptedWrite(data)
				},
			)

			go func() {
				for data := range hs.NotifyCh() {
					fs.HandleNotification(data)
				}
			}()

			fileList, err := fs.GetFileList(0, 10*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "File list failed: %v\n", err)
				os.Exit(1)
			}

			if len(fileList.Files) == 0 {
				fmt.Println("\nNo files on device.")
				return
			}

			fmt.Printf("\nFound %d file(s):\n", len(fileList.Files))
			for i, f := range fileList.Files {
				fmt.Printf("  [%d] session_id=%d size=%s attr=%d scene=%d\n",
					i, f.SessionID, protocol.HumanSize(int64(f.FileSize)), f.Attribute, f.Scene)
			}

			out := outputDir
			if out == "" {
				out = cfg.DownloadDir
			}
			os.MkdirAll(out, 0755)

			e2eeKey, err := cfg.GetE2EERSAPrivateKey()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: E2EE key not available, files will not be decrypted: %v\n", err)
			}

			filesToDownload := fileList.Files
			if !allFiles && sessionID >= 0 {
				filesToDownload = nil
				for _, f := range fileList.Files {
					if int(f.SessionID) == sessionID {
						filesToDownload = append(filesToDownload, f)
						break
					}
				}
				if len(filesToDownload) == 0 {
					fmt.Fprintf(os.Stderr, "Session ID %d not found\n", sessionID)
					os.Exit(1)
				}
			}

			for i, f := range filesToDownload {
				binPath := filepath.Join(out, fmt.Sprintf("recording_%d.bin", f.SessionID))
				oggPath := filepath.Join(out, fmt.Sprintf("recording_%d.ogg", f.SessionID))

				fmt.Printf("\nDownloading %d/%d: recording_%d (%s)\n",
					i+1, len(filesToDownload), f.SessionID, protocol.HumanSize(int64(f.FileSize)))

				_, err := fs.DownloadFile(f.SessionID, f.FileSize, binPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  Download failed: %v\n", err)
					continue
				}

				if e2eeKey != nil {
					if err := decryptFile(binPath, oggPath, e2eeKey); err != nil {
						fmt.Fprintf(os.Stderr, "  Decrypt failed: %v\n", err)
					} else {
						fmt.Printf("  Decrypted -> %s\n", oggPath)
					}
				}

				time.Sleep(500 * time.Millisecond)
			}

			fmt.Println("\nDone.")
		},
	}

	cmd.Flags().StringVarP(&address, "address", "a", "", "Device MAC address (auto-detect if omitted)")
	cmd.Flags().BoolVarP(&allFiles, "all", "", false, "Download all files")
	cmd.Flags().IntVarP(&sessionID, "session-id", "s", -1, "Specific session ID to download")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "Output directory (default: from config)")

	return cmd
}

func decryptCmd() *cobra.Command {
	var keyPath string

	cmd := &cobra.Command{
		Use:   "decrypt <file>",
		Short: "Decrypt a PLAUD.AI container to OGG",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			inputPath := args[0]
			data, err := os.ReadFile(inputPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
				os.Exit(1)
			}

			if !crypto.IsPLAUDContainer(data) {
				fmt.Fprintf(os.Stderr, "Not a PLAUD.AI container\n")
				os.Exit(1)
			}

			var rsaKey *rsa.PrivateKey
			if keyPath != "" {
				pemData, err := os.ReadFile(keyPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error reading key file: %v\n", err)
					os.Exit(1)
				}
				rsaKey, err = crypto.LoadRSAPrivateKey(string(pemData))
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error loading RSA key: %v\n", err)
					os.Exit(1)
				}
			} else {
				cfg := loadConfig()
				rsaKey, err = cfg.GetE2EERSAPrivateKey()
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error loading E2EE RSA key: %v\n", err)
					os.Exit(1)
				}
			}

			audio, err := crypto.DecryptPLAUDContainer(data, rsaKey)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Decryption failed: %v\n", err)
				os.Exit(1)
			}

			outputPath := strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + ".ogg"
			if err := os.WriteFile(outputPath, audio, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing output: %v\n", err)
				os.Exit(1)
			}

			fmt.Printf("Decrypted %d bytes -> %s\n", len(audio), outputPath)
		},
	}

	cmd.Flags().StringVar(&keyPath, "key", "", "RSA private key PEM file")

	return cmd
}

func transcribeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transcribe <file.ogg>",
		Short: "Transcribe an OGG file via whisper.cpp",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			cfg := loadConfig()
			t := whisper.NewTranscriber(
				cfg.Whisper.WhisperCliPath,
				cfg.Whisper.ModelPath,
				cfg.Whisper.Language,
				cfg.Whisper.LibPath,
				cfg.Whisper.GGMLLibPath,
				cfg.Whisper.VAD,
				cfg.Whisper.VADModelPath,
				cfg.Whisper.VADThreshold,
			)

			result, err := t.Transcribe(args[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Transcription failed: %v\n", err)
				os.Exit(1)
			}

			fmt.Printf("Transcription saved to %s\n", result.OutputFile)
			fmt.Printf("---\n%s\n", result.Text)
		},
	}

	return cmd
}

func oneshotCmd() *cobra.Command {
	var address string
	var noDelete bool
	var outputDir string

	cmd := &cobra.Command{
		Use:   "oneshot",
		Short: "Full pipeline: scan → download → decrypt → transcribe → delete",
		Long:  "Connects to a Plaud Note Pro device, downloads all recordings, decrypts them to OGG, transcribes with whisper.cpp, and deletes them from the device.",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := loadConfig()
			transport := ble.NewTransport()

			_, err := findAndConnect(transport, cfg, address)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Connection failed: %v\n", err)
				os.Exit(1)
			}
			defer transport.Disconnect()

			hs := handshake.NewHandshaker(transport, cfg)
			result, err := hs.DoHandshake()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Handshake failed: %v\n", err)
				os.Exit(1)
			}
			session := result.Session

			fs := filesync.NewFileSyncManager(
				func(data []byte) ([]byte, error) {
					return session.DecryptRecv(data)
				},
				func(data []byte) error {
					return hs.EncryptedWrite(data)
				},
			)

			go func() {
				for data := range hs.NotifyCh() {
					fs.HandleNotification(data)
				}
			}()

			fileList, err := fs.GetFileList(0, 10*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "File list failed: %v\n", err)
				os.Exit(1)
			}

			if len(fileList.Files) == 0 {
				fmt.Println("\nNo files on device.")
				return
			}

			out := outputDir
			if out == "" {
				out = cfg.DownloadDir
			}
			os.MkdirAll(out, 0755)

			fmt.Printf("\nFound %d file(s) on device\n", len(fileList.Files))

			e2eeKey, err := cfg.GetE2EERSAPrivateKey()
			if err != nil {
				fmt.Fprintf(os.Stderr, "E2EE key not available: %v\n", err)
				os.Exit(1)
			}

			t := whisper.NewTranscriber(
				cfg.Whisper.WhisperCliPath,
				cfg.Whisper.ModelPath,
				cfg.Whisper.Language,
				cfg.Whisper.LibPath,
				cfg.Whisper.GGMLLibPath,
				cfg.Whisper.VAD,
				cfg.Whisper.VADModelPath,
				cfg.Whisper.VADThreshold,
			)

			for i, f := range fileList.Files {
				fmt.Printf("\n--- File %d/%d: session_id=%d (%s) ---\n",
					i+1, len(fileList.Files), f.SessionID, protocol.HumanSize(int64(f.FileSize)))

				binPath := filepath.Join(out, fmt.Sprintf("recording_%d.bin", f.SessionID))
				oggPath := filepath.Join(out, fmt.Sprintf("recording_%d.ogg", f.SessionID))

				_, err := fs.DownloadFile(f.SessionID, f.FileSize, binPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  Download failed: %v\n", err)
					continue
				}

				if err := decryptFile(binPath, oggPath, e2eeKey); err != nil {
					fmt.Fprintf(os.Stderr, "  Decrypt failed: %v\n", err)
					continue
				}
				fmt.Printf("  Decrypted -> %s\n", oggPath)

				result, err := t.Transcribe(oggPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  Transcription failed: %v\n", err)
					continue
				}
				fmt.Printf("  Transcribed -> %s\n", result.OutputFile)
				fmt.Printf("  Text: %s\n", result.Text)

				if !noDelete {
					if err := fs.DeleteFile(f.SessionID); err != nil {
						fmt.Fprintf(os.Stderr, "  Delete failed: %v\n", err)
					} else {
						fmt.Printf("  Deleted from device\n")
					}
				}

				os.Remove(binPath)
				time.Sleep(500 * time.Millisecond)
			}

			fmt.Println("\n=== Oneshot complete ===")
		},
	}

	cmd.Flags().StringVarP(&address, "address", "a", "", "Device MAC address (auto-detect if omitted)")
	cmd.Flags().BoolVar(&noDelete, "no-delete", false, "Don't delete files from device after processing")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "Output directory (default: from config)")

	return cmd
}

func doctorCmd() *cobra.Command {
	var outputDir string

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Find and transcribe recordings missing transcriptions",
		Long:  "Scans the output directory for .ogg files that don't have a corresponding .txt transcription and re-runs whisper.cpp on them.",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := loadConfig()

			out := outputDir
			if out == "" {
				out = cfg.DownloadDir
			}

			entries, err := os.ReadDir(out)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading directory %s: %v\n", out, err)
				os.Exit(1)
			}

			oggFiles := make(map[string]string)
			txtFiles := make(map[string]bool)

			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				ext := strings.ToLower(filepath.Ext(name))
				base := strings.TrimSuffix(name, ext)
				switch ext {
				case ".ogg":
					oggFiles[base] = filepath.Join(out, name)
				case ".txt":
					txtFiles[base] = true
				}
			}

			var orphans []string
			for base, oggPath := range oggFiles {
				if !txtFiles[base] {
					orphans = append(orphans, oggPath)
				}
			}

			if len(orphans) == 0 {
				fmt.Println("All recordings have transcriptions. Nothing to fix.")
				return
			}

			fmt.Printf("Found %d recording(s) missing transcriptions:\n", len(orphans))
			for _, p := range orphans {
				fmt.Printf("  %s\n", filepath.Base(p))
			}

			t := whisper.NewTranscriber(
				cfg.Whisper.WhisperCliPath,
				cfg.Whisper.ModelPath,
				cfg.Whisper.Language,
				cfg.Whisper.LibPath,
				cfg.Whisper.GGMLLibPath,
				cfg.Whisper.VAD,
				cfg.Whisper.VADModelPath,
				cfg.Whisper.VADThreshold,
			)

			fixed := 0
			for i, oggPath := range orphans {
				fmt.Printf("\n[%d/%d] Transcribing %s ...\n", i+1, len(orphans), filepath.Base(oggPath))
				result, err := t.Transcribe(oggPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  Failed: %v\n", err)
					continue
				}
				fixed++
				fmt.Printf("  Saved -> %s\n", result.OutputFile)
				fmt.Printf("  Text: %s\n", result.Text)
			}

			fmt.Printf("\n=== Doctor complete: %d/%d fixed ===\n", fixed, len(orphans))
		},
	}

	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "Output directory (default: from config)")

	return cmd
}

func tuiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Launch interactive TUI for managing transcriptions",
		Long:  "An interactive terminal UI that connects to your Plaud device, auto-syncs recordings, and provides per-day transcription browsing with daily log building.",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := loadConfig()
			if err := tui.RunTUI(cfg); err != nil {
				fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
				os.Exit(1)
			}
		},
	}

	return cmd
}

func summarizeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "summarize <date>",
		Short: "Generate an LLM executive summary for a daily log",
		Long:  "Reads the daily log for the given date (YYYY-MM-DD), sends it to a configured LLM, and saves an executive summary. Overwrites any existing summary.",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			cfg := loadConfig()
			date := args[0]

			if !cfg.LLM.Configured() {
				fmt.Fprintf(os.Stderr, "LLM not configured. Add llm.api_key to your config file.\n")
				os.Exit(1)
			}

			logPath := filepath.Join(cfg.DownloadDir, "daily", date+".md")
			data, err := os.ReadFile(logPath)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Fprintf(os.Stderr, "No daily log found for %s. Build one from the TUI first.\n", date)
				} else {
					fmt.Fprintf(os.Stderr, "Error reading daily log: %v\n", err)
				}
				os.Exit(1)
			}

			logContent := string(data)
			if strings.TrimSpace(logContent) == "" {
				fmt.Fprintf(os.Stderr, "Daily log for %s is empty.\n", date)
				os.Exit(1)
			}

			var memoryContext string
			memStore, memErr := memory.Open(cfg.MemoryDBPath)
			if memErr != nil {
				log.Printf("warning: could not open memory store: %v", memErr)
			} else {
				defer memStore.Close()
				memStore.MaybePrune(cfg.MemoryMaxAgeDays)
				matched, _ := memStore.SearchByText(logContent)
				memoryContext = memory.RenderForPromptCompact(matched)
			}

			fmt.Printf("Generating summary for %s using %s (%s)...\n", date, cfg.LLM.Provider, cfg.LLM.EffectiveModel())

			client := llm.NewClient(cfg.LLM)
			summary, err := llm.GenerateSummary(cmd.Context(), client, logContent, memoryContext)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Summary generation failed: %v\n", err)
				os.Exit(1)
			}

			summaryPath := filepath.Join(cfg.DownloadDir, "daily", date+"_summary.md")
			if err := os.WriteFile(summaryPath, []byte(summary), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing summary: %v\n", err)
				os.Exit(1)
			}

			fmt.Printf("Summary saved to %s\n", summaryPath)

			if memStore != nil {
				fmt.Printf("Updating memory...\n")
				existingTerms, _ := memStore.AllTerms()
				newEntities, extErr := memory.ExtractEntities(cmd.Context(), client, logContent, existingTerms)
				if extErr != nil {
					log.Printf("memory entity extraction error: %v", extErr)
				} else if len(newEntities) > 0 {
					if upsertErr := memStore.UpsertBatch(newEntities, time.Now()); upsertErr != nil {
						log.Printf("memory upsert error: %v", upsertErr)
					} else {
						fmt.Printf("Learned %d new entities.\n", len(newEntities))
					}
				}
			}

			fmt.Printf("---\n%s\n", summary)
		},
	}

	return cmd
}

func findAndConnect(transport *ble.Transport, cfg *config.Config, address string) (string, error) {
	if address != "" {
		if err := transport.Connect(address); err != nil {
			return "", err
		}
		return address, nil
	}

	fmt.Printf("\nScanning for Plaud device (%.0fs)...\n", cfg.BLEScanDuration)
	devices, err := ble.Scan(cfg.BLEScanDuration)
	if err != nil {
		return "", fmt.Errorf("BLE scan: %w", err)
	}
	if len(devices) == 0 {
		return "", fmt.Errorf("no Plaud devices found. Make sure phone BT is off and device is awake")
	}

	fmt.Printf("\nFound %d device(s):\n", len(devices))
	for i, d := range devices {
		name := d.Name
		if name == "" {
			name = "(unknown)"
		}
		fmt.Printf("  [%d] %s  %s\n", i, d.Address.String(), name)
	}

	if len(devices) == 1 {
		addr := devices[0].Address.String()
		if err := transport.Connect(addr); err != nil {
			return "", err
		}
		return addr, nil
	}

	fmt.Print("Select device [0]: ")
	var idx int
	fmt.Scanln(&idx)
	if idx < 0 || idx >= len(devices) {
		return "", fmt.Errorf("invalid selection")
	}

	addr := devices[idx].Address.String()
	if err := transport.Connect(addr); err != nil {
		return "", err
	}
	return addr, nil
}

func decryptFile(inputPath, outputPath string, rsaKey *rsa.PrivateKey) error {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	if !crypto.IsPLAUDContainer(data) {
		return fmt.Errorf("not a PLAUD.AI container")
	}

	audio, err := crypto.DecryptPLAUDContainer(data, rsaKey)
	if err != nil {
		return fmt.Errorf("decrypting: %w", err)
	}

	return os.WriteFile(outputPath, audio, 0644)
}

// Suppress unused import warnings
var _ = base64.StdEncoding
var _ = protocol.CmdName
