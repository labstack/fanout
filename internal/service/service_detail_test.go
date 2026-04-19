package service

import (
	"context"
	"fmt"
	"testing"
)

func TestServiceDetail_DiagnoseError(t *testing.T) {
	svc, sqlMock := newMockService(t)
	defer svc.duck.DB.Close()

	sqlMock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db error"))

	_, err := svc.ServiceDetail(context.Background(), "bad-service", 60, "")
	if err == nil {
		t.Fatal("expected error from diagnose failure")
	}
}
