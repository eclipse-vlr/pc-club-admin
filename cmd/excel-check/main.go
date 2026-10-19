// excel-check verifies server-account access without changing Drive or the DB.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"pc-club-admin/internal/excelsync"
)

func main() {
	credentials := flag.String("credentials", os.Getenv("DRIVE_EXCEL_CREDENTIALS_FILE"), "local path to the service account JSON; never the key contents")
	fileID := flag.String("file-id", os.Getenv("DRIVE_EXCEL_FILE_ID"), "ID of the test XLSX copy")
	flag.Parse()
	if flag.NArg() != 0 || (*credentials) == "" || (*fileID) == "" || os.Getenv("APP_ENV") != "" && os.Getenv("APP_ENV") != "test" {
		fmt.Fprintln(os.Stderr, "Укажите -credentials и -file-id тестовой копии; разрешён только APP_ENV=test.")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := excelsync.NewDriveClient(ctx, *credentials, *fileID)
	if err == nil {
		defer client.Close()
		err = client.Check(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Проверка Drive не пройдена:", excelsync.Code(err))
		os.Exit(1)
	}
	fmt.Println("Доступ сервисного аккаунта, XLSX-шаблон, контрольная сумма и ETag проверены. Файл не изменён. Синхронизация не включена.")
	fmt.Println("Условную запись и повтор после сбоя ещё нужно проверить отдельно на тестовой копии.")
}
