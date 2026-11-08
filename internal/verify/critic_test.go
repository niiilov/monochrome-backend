package verify

import (
	"context"
	"strings"
	"testing"

	"mono/internal/domain"
	"mono/internal/llm/mock"
)

func TestGateCriticDisabledDoesNotCall(t *testing.T) {
	w := sample()
	calls := 0
	stub := criticFunc(func(ctx context.Context, item domain.DigestItem, arts map[string]domain.Article) ([]Finding, error) {
		calls++
		return []Finding{{
			Rule: RuleCritic, Severity: SevError, Message: "не должно вызываться",
		}}, nil
	})
	res, err := Gate(context.Background(), nil, w.model, w.events, w.arts, Options{
		MaxRetries: 2,
		Critic:     stub,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("critic called %d times while disabled", calls)
	}
	denyErrors(t, res.Findings)
}

func TestGateCriticUnsupportedTriggersRewrite(t *testing.T) {
	w := sample()
	w.model = cloneModel(w.model)
	w.model.Items[0].Essence = "Утекли 20 записей. Дополнительно якобы украли 777 паролей."
	first := true
	flex := criticFunc(func(ctx context.Context, item domain.DigestItem, arts map[string]domain.Article) ([]Finding, error) {
		if first {
			first = false
			return []Finding{{
				Rule: RuleCritic, Severity: SevError, Where: "event:E01/essence",
				Message: "критик: не подтверждено источником: 777 паролей",
			}}, nil
		}
		return nil, nil
	})
	client := mock.New()
	client.When("write_digest_item", "", itemPayload(
		"Утечка 20 записей",
		"В сообщении Роскомнадзор 6 ноября 2026 подтвердил: утекли 20 записей. Компания заявила о проверке. «данные скомпрометированы»",
		"",
		"",
	))
	res, err := Gate(context.Background(), client, w.model, w.events, w.arts, Options{
		MaxRetries:    1,
		CriticEnabled: true,
		Critic:        flex,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Report.Retries != 1 {
		t.Fatalf("retries=%d", res.Report.Retries)
	}
	if client.CallsFor("write_digest_item") != 1 {
		t.Fatalf("rewrite calls=%d", client.CallsFor("write_digest_item"))
	}
	if strings.Contains(res.DigestMD, "777") {
		t.Fatalf("unsupported claim remained:\n%s", res.DigestMD)
	}
	denyErrors(t, res.Findings)
}

type criticFunc func(ctx context.Context, item domain.DigestItem, arts map[string]domain.Article) ([]Finding, error)

func (f criticFunc) CheckItem(ctx context.Context, item domain.DigestItem, arts map[string]domain.Article) ([]Finding, error) {
	return f(ctx, item, arts)
}
