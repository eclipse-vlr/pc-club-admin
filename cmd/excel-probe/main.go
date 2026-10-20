// excel-probe verifies conditional uploads on an explicitly confirmed disposable copy.
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
	confirm := flag.String("confirm-copy-id", "", "repeat the disposable copy ID to authorize a new revision")
	flag.Parse()
	if flag.NArg() != 0 || *confirm != *fileID || (*credentials) == "" || (*fileID) == "" || os.Getenv("APP_ENV") != "test" {
		fmt.Fprintln(os.Stderr, "Укажите -credentials, -file-id и совпадающий -confirm-copy-id одноразовой копии; разрешён только APP_ENV=test.")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := excelsync.NewDriveClient(ctx, *credentials, *fileID)
	if err == nil {
		defer client.Close()
		err = client.ProbeUpload(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Проверка Drive не пройдена:", excelsync.Code(err))
		os.Exit(1)
	}
	fmt.Println("Drive отклонил неверный ETag ответом HTTP 412 и принял актуальный ETag. Создана ревизия с тем же содержимым копии.")
	fmt.Println("Синхронизация выключена. Повтор доставки после сбоя и отсутствие дубликатов этой командой не проверяются.")
}
