package domain

import (
	"errors"
	"strings"
	"time"
)

// Note is a named markdown document in a project; org-wide notes live in the
// org's global project. Its slug is the human handle and is immutable;
// its id is the stable key. Status uses RecordActive and RecordArchived.
type Note struct {
	ID        ID
	OrgID     ID
	ProjectID ID
	Status    RecordStatus
	Version   int64
	CreatedBy ActorRef
	UpdatedBy ActorRef
	CreatedAt time.Time
	UpdatedAt time.Time
	Slug      string
	Title     string
	Body      string
	// PageBytes is the size of the note's HTML page; 0 when it has none.
	PageBytes int
}

// Validate checks a note's own fields.
func (n Note) Validate() error {
	var errs []error
	if err := ValidateSlug("slug", n.Slug); err != nil {
		errs = append(errs, err)
	}
	if strings.TrimSpace(n.Title) == "" {
		errs = append(errs, invalid("title", "required"))
	}
	if len(n.Title) > 200 {
		errs = append(errs, invalid("title", "at most 200 characters"))
	}
	if strings.TrimSpace(n.Body) == "" && n.PageBytes == 0 {
		errs = append(errs, invalid("body", "required unless the note has a page"))
	}
	if len(n.Body) > 256*1024 {
		errs = append(errs, invalid("body", "at most 256 KiB"))
	}
	return errors.Join(errs...)
}

// NoteRevision is the title, body and page a note had before an edit replaced
// them. Its time is in its id. Page is empty when that version had none.
type NoteRevision struct {
	ID        ID
	NoteID    ID
	Version   int64
	UpdatedBy ActorRef
	Title     string
	Body      string
	Page      string
}
