package critic

import (
	"time"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/summarize"
	"cyberdigest/internal/timeutil"
)

const (
	// Semantic lies without new numbers, URLs or quotes so deterministic
	// verify does not remove them before the critic runs.
	plantedLieCode    = "украден исходный код биллинга"
	plantedLieOpen    = "диагнозы были опубликованы в открытом доступе"
	plantedLieRansom  = "выкуп составил два миллиона долларов"
)

// BuiltinCases returns the train-like grounded case and the planted-error case.
func BuiltinCases() []CaseSpec {
	return []CaseSpec{trainCase(), plantedCase()}
}

func trainCase() CaseSpec {
	published := time.Date(2026, 11, 6, 15, 20, 0, 0, timeutil.MSK)
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, timeutil.MSK)
	end := time.Date(2026, 11, 7, 12, 0, 0, 0, timeutil.MSK)
	const (
		id  = "train-01"
		url = "https://tass.ru/example/train-01"
	)
	article := domain.Article{
		ID:          id,
		Title:       "Утечка «Медкарта-Онлайн»: 840 000 записей",
		Text:        "ТАСС 6 ноября 2026 года сообщил об утечке сервиса «Медкарта-Онлайн». По данным компании, наружу попали 840 000 записей: фамилии, полисы ОМС и телефоны. Пароли и диагнозы, как утверждает сервис, в выгрузке отсутствуют.",
		Source:      "ТАСС",
		URL:         url,
		PublishedAt: &published,
		SourceKind:  domain.SourceMedia,
	}
	event := domain.Event{
		ID:               "E01",
		Rank:             1,
		Selected:         true,
		ArticleIDs:       []string{id},
		PrimaryArticleID: id,
		Rationale:        "Утечка затрагивает персональные данные пациентов.",
		Mentions: []domain.Mention{{
			ArticleID:  id,
			TitleDraft: "Утечка 840 000 записей",
			Hedges:     []string{"как утверждает сервис"},
			Facts: []domain.Fact{{
				Text:      "Наружу попали 840 000 записей.",
				Quote:     "наружу попали 840 000 записей",
				ArticleID: id,
			}},
		}},
	}
	arts := map[string]domain.Article{id: article}
	item := domain.DigestItem{
		N:            1,
		EventID:      "E01",
		Title:        "Утечка 840 000 записей в «Медкарта-Онлайн»",
		Essence:      "6 ноября 2026 ТАСС сообщил об утечке сервиса «Медкарта-Онлайн»: наружу попали 840 000 записей с фамилиями, полисами ОМС и телефонами. Как утверждает сервис, пароли и диагнозы в выгрузке отсутствуют.",
		Consequences: summarize.NoConsequences,
		Sources:      summarize.SourceRefs(event, arts),
		Rationale:    event.Rationale,
	}
	return CaseSpec{
		Name:  CaseTrain,
		Title: "учебный пересказ без подсаженных ошибок",
		Model: domain.DigestModel{
			Period:      domain.Period{Start: start, End: end},
			ControlTime: end,
			Items:       []domain.DigestItem{item},
			Shortfall:   "в окне одна подтвержденная публикация",
		},
		Events:    []domain.Event{event},
		Articles:  arts,
		Forbidden: nil,
		Required:  []string{"840 000", "Медкарта-Онлайн"},
	}
}

func plantedCase() CaseSpec {
	base := trainCase()
	base.Name = CasePlanted
	base.Title = "синтетика с подсаженными ошибками в пересказе"
	item := base.Model.Items[0]
	item.Title = "Утечка 840 000 записей в «Медкарта-Онлайн»"
	item.Essence = "6 ноября 2026 ТАСС сообщил об утечке сервиса «Медкарта-Онлайн»: наружу попали 840 000 записей. Как утверждает сервис, пароли в выгрузке отсутствуют. Также " + plantedLieCode + ". Позднее утверждалось, что " + plantedLieOpen + ". Отдельно писали, что " + plantedLieRansom + "."
	base.Model.Items = []domain.DigestItem{item}
	base.Forbidden = []string{plantedLieCode, plantedLieOpen, plantedLieRansom}
	base.Required = []string{"840 000"}
	base.EtalonMiss = 3
	return base
}
