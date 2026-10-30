package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/valpere/news-library/internal/library"
	"time"
)

func archiveCommand(command string, args []string) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	source := flags.String("source", "", "каталог даних для backup або резервна копія для restore/verify-backup")
	output := flags.String("output", "", "новий неіснуючий каталог")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *source == "" || flags.NArg() != 0 || (command != "verify-backup" && *output == "") {
		return errors.New("archive command requires -source and (backup/restore) -output")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	switch command {
	case "backup":
		m, err := library.Backup(ctx, *source, *output)
		if err != nil {
			return err
		}
		fmt.Printf("backup complete: %s (%d files, schema %d)\n", *output, len(m.Files), m.Schema)
	case "verify-backup":
		m, err := library.VerifyBackup(*source)
		if err != nil {
			return err
		}
		fmt.Printf("backup verified: %d files, schema %d\n", len(m.Files), m.Schema)
	case "restore":
		if err := library.Restore(ctx, *source, *output); err != nil {
			return err
		}
		fmt.Printf("restore complete: %s; background execution paused\n", *output)
	}
	return nil
}
