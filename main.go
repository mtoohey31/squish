package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/mholt/archives"
)

var cli struct {
	Create struct {
		Output string   `arg:"" help:"The path of the archive or compressed file to create."`
		Inputs []string `arg:"" optional:"" help:"The files to include in the output. Exactly one input must be provided when the output is a compressed file."`
	} `cmd:"" help:"Create an archive or compressed file."`
	Extract struct {
		Input  string  `arg:"" help:"The path of the archive or compressed to extract from."`
		Output *string `arg:"" optional:"" help:"The directory to extract archive entries to, or the file to write the decompressed contents to."`
	} `cmd:"" help:"Extract files from an archive or compressed file."`
}

func main() {
	ctx := context.Background()

	exitCode := 0
	defer func() { os.Exit(exitCode) }()

	bail := func(format string, a ...any) {
		_, err := fmt.Fprintf(os.Stderr, format+"\n", a...)
		if err != nil {
			panic(err)
		}
		exitCode = 1
		runtime.Goexit()
	}

	switch kong.Parse(&cli).Selected().Name {
	case "create":
		filenames := map[string]string{}
		for _, file := range cli.Create.Inputs {
			filenames[file] = file
		}
		files, err := archives.FilesFromDisk(ctx, nil, filenames)
		if err != nil {
			bail("failed to discover files: %s", err)
		}

		format, _, err := archives.Identify(ctx, cli.Create.Output, nil)
		if err != nil {
			bail("failed to identify format: %s", err)
		}

		switch format := format.(type) {
		case archives.Archiver:
			output, err := os.Create(cli.Create.Output)
			if err != nil {
				bail("failed to create archive file: %s", err)
			}
			defer func() {
				if err := output.Close(); err != nil {
					bail("failed to close archive file: %s", err)
				}
			}()

			if err := format.Archive(ctx, output, files); err != nil {
				bail("failed to create archive: %s", err)
			}

		case archives.Compressor:
			if len(files) < 1 {
				bail("identified format only supports compression, but no input file was provided")
			}
			if len(files) > 1 {
				bail("identified format only supports compression, but multiple input files were provided")
			}

			output, err := os.Create(cli.Create.Output)
			if err != nil {
				bail("failed to create compressed file: %s", err)
			}
			defer func() {
				if err := output.Close(); err != nil {
					bail("failed to close compressed file: %s", err)
				}
			}()

			outputWC, err := format.OpenWriter(output)
			if err != nil {
				bail("failed to create compressed file writer: %s", err)
			}
			defer func() {
				if err := outputWC.Close(); err != nil {
					bail("failed to close compressed file writer: %s", err)
				}
			}()

			input, err := files[0].Open()
			if err != nil {
				bail("failed to open input file: %s", err)
			}
			defer func() {
				if err := input.Close(); err != nil {
					bail("failed to open input file: %s", err)
				}
			}()

			if _, err := io.Copy(outputWC, input); err != nil {
				bail("failed to copy input file to compressed file writer: %s", err)
			}

		default:
			bail("identified format doesn't support archiving or compression")
		}

	case "extract":
		input, err := os.Open(cli.Extract.Input)
		if err != nil {
			bail("failed to open input file: %s", err)
		}
		defer func() {
			if err := input.Close(); err != nil {
				bail("failed to close input file: %s", err)
			}
		}()

		format, inputR, err := archives.Identify(ctx, cli.Create.Output, input)
		if err != nil {
			bail("failed to identify format: %s", err)
		}

		var output string
		if cli.Extract.Output != nil {
			output = *cli.Extract.Output
		} else if strings.HasSuffix(cli.Extract.Input, format.Extension()) {
			output = strings.TrimSuffix(cli.Extract.Input, format.Extension())
		} else if ext := filepath.Ext(cli.Extract.Input); ext != "" {
			output = strings.TrimSuffix(cli.Extract.Input, ext)
		} else {
			bail("failed to determine output path from input path and format, please specify it manually")
		}

		switch format := format.(type) {
		case archives.Extractor:
			if err := os.RemoveAll(output); err != nil {
				bail("failed to remove existing output: %s", err)
			}

			if err := os.Mkdir(output, 0o755); err != nil {
				bail("failed to create output directory: %s", err)
			}

			err := format.Extract(ctx, inputR, func(ctx context.Context, info archives.FileInfo) (err error) {
				cleanedName := filepath.Clean(info.NameInArchive)
				if !filepath.IsLocal(cleanedName) {
					return fmt.Errorf("input entry %s was non-local, potential directory traversal attack", info.NameInArchive)
				}

				joinedName := filepath.Join(output, cleanedName)

				if info.IsDir() {
					if err := os.Mkdir(joinedName, info.Mode()); err != nil {
						return fmt.Errorf("failed to create output directory: %s", err)
					}

					return nil
				}

				input, err := info.Open()
				if err != nil {
					return fmt.Errorf("failed to open input entry reader: %w", err)
				}
				defer func() {
					if closeErr := input.Close(); closeErr != nil {
						if err == nil {
							err = closeErr
						} else {
							fmt.Fprintf(os.Stderr, "failed to close input entry reader: %s\n", closeErr)
						}
					}
				}()

				output, err := os.OpenFile(joinedName, os.O_CREATE|os.O_WRONLY, info.Mode())
				if err != nil {
					return fmt.Errorf("failed to create output file: %s", err)
				}
				defer func() {
					if closeErr := output.Close(); closeErr != nil {
						if err == nil {
							err = closeErr
						} else {
							fmt.Fprintf(os.Stderr, "failed to close output file: %s\n", closeErr)
						}
					}
				}()

				if _, err := io.Copy(output, input); err != nil {
					return fmt.Errorf("failed to copy input entry to output file: %s", err)
				}

				return nil
			})
			if err != nil {
				bail("failed to extract archive: %s", err)
			}

		case archives.Decompressor:
			inputRC, err := format.OpenReader(inputR)
			if err != nil {
				bail("failed to create decompressor reader: %s", err)
			}
			defer func() {
				if err := inputRC.Close(); err != nil {
					bail("failed to close decompressor reader: %s", err)
				}
			}()

			output, err := os.Create(output)
			if err != nil {
				bail("failed to create output file: %s", err)
			}
			defer func() {
				if err := output.Close(); err != nil {
					bail("failed to close output file: %s", err)
				}
			}()

			if _, err := io.Copy(output, inputRC); err != nil {
				bail("failed to copy input to output file: %s", err)
			}

		default:
			bail("identified format doesn't support extraction or decompression")
		}

	default:
		panic("unknown subcommand")
	}
}
