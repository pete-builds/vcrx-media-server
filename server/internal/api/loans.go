package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/pete-builds/vcrx-media-server/server/internal/invites"
	"github.com/pete-builds/vcrx-media-server/server/internal/loans"
)

// Lend a tape (milestone 10, item 1). An admin lends one movie; the answer
// carries a one-time invite for the printed card. See package loans for the
// model; the rules themselves are in the one policy path.

func (s *Server) adminLoans(w http.ResponseWriter, r *http.Request) {
	list, err := s.Loans.List(r.Context(), queryInt(r, "limit", 200))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"loans": list})
}

// adminLend lends a movie. Its invite secret is in this answer and nowhere
// else, like any invite's.
func (s *Server) adminLend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ItemID         int64  `json:"item_id"`
		Name           string `json:"name"`
		Hours          int    `json:"hours"`
		Allow4K        bool   `json:"allow_4k"`
		AllowTranscode bool   `json:"allow_transcode"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	by := info(r).Session.User.Username
	l, secret, cut, err := s.Loans.Lend(r.Context(), by, loans.Request{ItemID: req.ItemID, Borrower: req.Name, Hours: req.Hours,
		Allow4K: req.Allow4K, AllowTranscode: req.AllowTranscode})
	switch {
	case errors.Is(err, loans.ErrOut):
		writeError(w, http.StatusConflict, "on_loan", "This tape is already out on loan. Return it first.")
		return
	case errors.Is(err, loans.ErrNotLendable):
		writeError(w, http.StatusUnprocessableEntity, "not_lendable", "Only a movie that is on disk can be lent.")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	stopped := s.cutStreams(cut)
	link := s.origin(r) + "/#/invite/" + secret
	png, err := invites.QRPNG(link)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.Log.Info("tape lent", "by", by, "loan", l.ID, "item", req.ItemID, "device_token", l.TokenID, "due", l.DueAt, "streams_stopped", stopped)
	writeJSON(w, http.StatusCreated, map[string]any{"loan": l, "invite": secret, "url": link,
		"qr_png": base64.StdEncoding.EncodeToString(png), "streams_stopped": stopped})
}

func (s *Server) adminReturnLoan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "loan_id")
	if !ok {
		return
	}
	by := info(r).Session.User.Username
	cut, err := s.returnLoan(r.Context(), id, by)
	if errors.Is(err, loans.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No such loan.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	l, err := s.Loans.Get(r.Context(), id)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"loan": l, "streams_stopped": cut})
}

// returnLoan ends a loan now and cuts the borrower's open streams, through
// the same stream registry Activity's Stop uses.
func (s *Server) returnLoan(ctx context.Context, id int64, by string) (int, error) {
	open, err := s.Loans.Return(ctx, id, by)
	if err != nil {
		return 0, err
	}
	cut := s.cutStreams(open)
	s.Log.Info("tape returned", "loan", id, "by", by, "open_streams", cut)
	return cut, nil
}

// loanEnded answers a borrower's device whose loan is over with "Be kind,
// rewind." rather than a bare "sign in again", so its screen can say so. It
// reports false, writing nothing, for any other refused token.
func (s *Server) loanEnded(w http.ResponseWriter, r *http.Request, tok string) bool {
	if s.Loans == nil {
		return false
	}
	l, ok, err := s.Loans.Ended(r.Context(), tok)
	if err != nil {
		s.Log.Warn("look up an ended loan", "err", err)
		return false
	}
	if !ok {
		return false
	}
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "loan_ended", "message": loans.RewindMessage(l.Title),
		"title": l.Title, "state": l.State})
	return true
}

// meLoan is a borrower's loan, as /me shows it to the borrower's device.
type meLoan struct {
	ItemID *int64 `json:"item_id"`
	Title  string `json:"title"`
	DueAt  int64  `json:"due_at"`
}

func (s *Server) borrowerLoan(ctx context.Context, tokenID int64) (*meLoan, error) {
	if s.Loans == nil || tokenID == 0 {
		return nil, nil
	}
	l, ok, err := s.Loans.ForToken(ctx, tokenID)
	if err != nil || !ok {
		return nil, err
	}
	return &meLoan{ItemID: l.ItemID, Title: l.Title, DueAt: l.DueAt}, nil
}
