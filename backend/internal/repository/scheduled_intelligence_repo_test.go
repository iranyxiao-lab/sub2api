package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestScheduledTestClaimIsAtomic(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := &scheduledTestPlanRepository{db: db}
	until := time.Now().Add(10 * time.Minute)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE scheduled_test_plans SET claimed_until = $2")).WithArgs(int64(7), until, "owner-1", true).WillReturnResult(sqlmock.NewResult(0, 1))
	claimed, err := repo.TryClaim(context.Background(), 7, until, "owner-1", true)
	if err != nil || !claimed {
		t.Fatalf("first claim: %v, %v", claimed, err)
	}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE scheduled_test_plans SET claimed_until = $2")).WithArgs(int64(7), until, "owner-2", true).WillReturnResult(sqlmock.NewResult(0, 0))
	claimed, err = repo.TryClaim(context.Background(), 7, until, "owner-2", true)
	if err != nil || claimed {
		t.Fatalf("duplicate claim: %v, %v", claimed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
