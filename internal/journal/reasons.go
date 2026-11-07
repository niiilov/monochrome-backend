package journal

import (
	"strconv"
	"strings"

	"cyberdigest/internal/domain"
	"cyberdigest/internal/timeutil"
)

// Data fills the reason templates from the specification.
type Data struct {
	Rationale   string
	EventID     string
	ModelReason string
	Period      domain.Period
	Place       int
	Total       int
	Why         string
}

// StatusFor is the journal status that belongs to a reason code.
func StatusFor(code domain.ReasonCode) (domain.Status, bool) {
	switch code {
	case domain.ReasonSelected:
		return domain.StatusIncluded, true
	case domain.ReasonMerged:
		return domain.StatusMerged, true
	case domain.ReasonOffTopic, domain.ReasonAdvertising, domain.ReasonOutOfPeriod,
		domain.ReasonDateUnconfirmed, domain.ReasonStaleNoNewFacts, domain.ReasonInsufficient,
		domain.ReasonLowerPriority, domain.ReasonReadError, domain.ReasonEmptyInput,
		domain.ReasonDuplicateID, domain.ReasonSourceDown:
		return domain.StatusExcluded, true
	default:
		return "", false
	}
}

// Text renders the Russian reason for code. The error column is filled by the caller, not here.
func Text(code domain.ReasonCode, data Data) string {
	switch code {
	case domain.ReasonSelected:
		why := strings.TrimSpace(data.Rationale)
		if why == "" {
			return "Событие вошло в выпуск"
		}
		return "Событие вошло в выпуск: " + why
	case domain.ReasonMerged:
		id := strings.TrimSpace(data.EventID)
		if id == "" {
			id = "без номера"
		}
		return "Сообщение о том же событии (" + id + "), ссылка сохранена в источниках"
	case domain.ReasonOffTopic:
		why := strings.TrimSpace(data.ModelReason)
		if why == "" {
			return "Материал не относится к теме информационной безопасности"
		}
		return "Материал не относится к теме информационной безопасности: " + why
	case domain.ReasonAdvertising:
		return "Рекламный материал, совпадение терминов не делает его новостью"
	case domain.ReasonOutOfPeriod:
		return "Публикация вне периода " + timeutil.FormatPeriod(data.Period)
	case domain.ReasonDateUnconfirmed:
		return "Дату публикации подтвердить нельзя, материал исключён из основного дайджеста"
	case domain.ReasonStaleNoNewFacts:
		return "Повтор раннего события без новых фактов"
	case domain.ReasonInsufficient:
		return "Данных для пересказа недостаточно"
	case domain.ReasonLowerPriority:
		why := strings.TrimSpace(data.Why)
		if why == "" {
			return formatPlace(data.Place, data.Total)
		}
		return formatPlace(data.Place, data.Total) + ", " + why
	case domain.ReasonReadError:
		return "Не удалось прочитать запись"
	case domain.ReasonEmptyInput:
		return "Пустой файл или запись"
	case domain.ReasonDuplicateID:
		return "Повторный ID в наборе, использована первая запись"
	case domain.ReasonSourceDown:
		return "Источник недоступен"
	default:
		return ""
	}
}

func formatPlace(place, total int) string {
	return "Уступило по приоритету: место " + strconv.Itoa(place) + " из " + strconv.Itoa(total)
}
