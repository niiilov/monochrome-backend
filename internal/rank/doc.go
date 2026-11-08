// Package rank scores events and keeps at most the configured number.
//
// Score asks the model for marks from 0 to 3 with a verbatim quote for each
// criterion. A mark is dropped when the quote is not a substring of the event
// articles or the value is outside 0..3. Total is the weighted mean of the
// marks that remain: a missing mark does not pull the total up or down.
// When any mark is nil, the journal trace records "часть данных неизвестна".
//
// Select orders by Total, then by more independent sources, then by a later
// publication time, then by event ID. It does not read a search rank.
// SelectPreferred uses the same order after placing events that contain a
// user keyword ahead of the others.
// ApplyJournal writes the digest reason for the chosen events and
// lower_priority, with place and score, for the rest.
package rank
