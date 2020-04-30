package operations

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/treeverse/lakefs/httputil"
	"github.com/treeverse/lakefs/logging"

	"github.com/treeverse/lakefs/permissions"

	"github.com/treeverse/lakefs/db"

	"github.com/treeverse/lakefs/gateway/errors"
	"github.com/treeverse/lakefs/gateway/path"
	"github.com/treeverse/lakefs/gateway/serde"
	indexErrors "github.com/treeverse/lakefs/index/errors"

	"github.com/treeverse/lakefs/index/model"

	"golang.org/x/xerrors"
)

const (
	ListObjectMaxKeys = 1000
)

type ListObjects struct{}

func (controller *ListObjects) Action(repoId, refId, path string) permissions.Action {
	return permissions.ListObjects(repoId)
}

func (controller *ListObjects) getMaxKeys(o *RepoOperation) int {
	params := o.Request.URL.Query()
	maxKeys := ListObjectMaxKeys
	if len(params.Get("max-keys")) > 0 {
		parsedKeys, err := strconv.ParseInt(params.Get("max-keys"), 10, 64)
		if err == nil {
			maxKeys = int(parsedKeys)
		}
	}
	return maxKeys
}

func (controller *ListObjects) serializeEntries(ref string, entries []*model.Entry) ([]serde.CommonPrefixes, []serde.Contents, string) {
	dirs := make([]serde.CommonPrefixes, 0)
	files := make([]serde.Contents, 0)
	var lastKey string
	for _, entry := range entries {
		lastKey = entry.GetName()
		switch entry.GetType() {
		case model.EntryTypeTree:
			dirs = append(dirs, serde.CommonPrefixes{Prefix: path.WithRef(entry.GetName(), ref)})
		case model.EntryTypeObject:
			files = append(files, serde.Contents{
				Key:          path.WithRef(entry.GetName(), ref),
				LastModified: serde.Timestamp(entry.CreationDate),
				ETag:         httputil.ETag(entry.Checksum),
				Size:         entry.Size,
				StorageClass: "STANDARD",
			})
		}
	}
	return dirs, files, lastKey
}

func (controller *ListObjects) serializeBranches(branches []*model.Branch) ([]serde.CommonPrefixes, string) {
	dirs := make([]serde.CommonPrefixes, 0)
	var lastKey string
	for _, branch := range branches {
		lastKey = branch.Id
		dirs = append(dirs, serde.CommonPrefixes{Prefix: path.WithRef("", branch.Id)})
	}
	return dirs, lastKey
}

func (controller *ListObjects) ListV2(o *RepoOperation) {
	o.AddLogFields(logging.Fields{
		"list_type": "v2",
	})
	params := o.Request.URL.Query()
	delimiter := params.Get("delimiter")
	startAfter := params.Get("start-after")
	continuationToken := params.Get("continuation-token")

	// resolve "from"
	var fromStr string
	if len(startAfter) > 0 {
		fromStr = startAfter
	}
	if len(continuationToken) > 0 {
		// take this instead
		fromStr = continuationToken
	}

	var from path.ResolvedPath

	maxKeys := controller.getMaxKeys(o)

	// see if this is a recursive call`
	descend := true
	if len(delimiter) >= 1 {
		if delimiter != path.Separator {
			// we only support "/" as a delimiter
			o.EncodeError(errors.Codes.ToAPIErr(errors.ErrBadRequest))
			return
		}
		descend = false
	}

	var results []*model.Entry
	hasMore := false

	var ref string

	// should we list branches?
	prefix, err := path.ResolvePath(params.Get("prefix"))
	if err != nil {
		o.Log().
			WithError(err).
			WithField("path", params.Get("prefix")).
			Error("could not resolve path for prefix")
		o.EncodeError(errors.Codes.ToAPIErr(errors.ErrBadRequest))
		return
	}

	if !prefix.WithPath {
		// list branches then.
		branchPrefix := prefix.Ref // TODO: same prefix logic also in V1!!!!!
		o.Log().WithField("prefix", branchPrefix).Debug("listing branches with prefix")
		branches, hasMore, err := o.Index.ListBranchesByPrefix(o.Repo.Id, branchPrefix, maxKeys, fromStr)
		if err != nil {
			o.Log().WithError(err).Error("could not list branches")
			o.EncodeError(errors.Codes.ToAPIErr(errors.ErrInternalError))
			return
		}
		// return branch response
		dirs, lastKey := controller.serializeBranches(branches)
		resp := serde.ListObjectsV2Output{
			Name:           o.Repo.Id,
			Prefix:         params.Get("prefix"),
			Delimiter:      delimiter,
			KeyCount:       len(dirs),
			MaxKeys:        maxKeys,
			CommonPrefixes: dirs,
			Contents:       make([]serde.Contents, 0),
		}

		if len(continuationToken) > 0 && strings.EqualFold(continuationToken, fromStr) {
			resp.ContinuationToken = continuationToken
		}

		if hasMore {
			resp.IsTruncated = true
			resp.NextContinuationToken = lastKey
		}

		o.EncodeResponse(resp, http.StatusOK)
		return

	} else {
		ref = prefix.Ref
		if len(fromStr) > 0 {
			from, err = path.ResolvePath(fromStr)
			if err != nil || !strings.EqualFold(from.Ref, prefix.Ref) {
				o.Log().WithError(err).WithFields(logging.Fields{
					"branch": prefix.Ref,
					"path":   prefix.Path,
					"from":   fromStr,
				}).Error("invalid marker - doesnt start with branch name")
				o.EncodeError(errors.Codes.ToAPIErr(errors.ErrBadRequest))
				return
			}
		}

		results, hasMore, err = o.Index.ListObjectsByPrefix(
			o.Repo.Id,
			prefix.Ref,
			prefix.Path,
			from.Path,
			maxKeys,
			descend)
		if xerrors.Is(err, db.ErrNotFound) {
			if xerrors.Is(err, indexErrors.ErrBranchNotFound) {
				o.Log().WithError(err).WithFields(logging.Fields{
					"ref":  prefix.Ref,
					"path": prefix.Path,
				}).Debug("could not list objects in path")
			}
			results = make([]*model.Entry, 0) // no results found
		} else if err != nil {
			o.Log().WithError(err).WithFields(logging.Fields{
				"ref":  prefix.Ref,
				"path": prefix.Path,
			}).Error("could not list objects in path")
			o.EncodeError(errors.Codes.ToAPIErr(errors.ErrBadRequest))
			return
		}
	}

	dirs, files, lastKey := controller.serializeEntries(ref, results)

	resp := serde.ListObjectsV2Output{
		Name:           o.Repo.Id,
		Prefix:         params.Get("prefix"),
		Delimiter:      delimiter,
		KeyCount:       len(results),
		MaxKeys:        maxKeys,
		CommonPrefixes: dirs,
		Contents:       files,
	}

	if len(continuationToken) > 0 && strings.EqualFold(continuationToken, fromStr) {
		resp.ContinuationToken = continuationToken
	}

	if hasMore {
		resp.IsTruncated = true
		resp.NextContinuationToken = path.WithRef(lastKey, ref)
	}

	o.EncodeResponse(resp, http.StatusOK)
}

func (controller *ListObjects) ListV1(o *RepoOperation) {
	o.AddLogFields(logging.Fields{
		"list_type": "v1",
	})

	// handle ListObjects (v1)
	params := o.Request.URL.Query()
	delimiter := params.Get("delimiter")
	descend := true

	if len(delimiter) >= 1 {
		if delimiter != path.Separator {
			// we only support "/" as a delimiter
			o.EncodeError(errors.Codes.ToAPIErr(errors.ErrBadRequest))
			return
		}
		descend = false
	}

	maxKeys := controller.getMaxKeys(o)

	var results []*model.Entry
	hasMore := false

	var ref string
	// should we list branches?
	prefix, err := path.ResolvePath(params.Get("prefix"))
	if err != nil {
		o.Log().
			WithError(err).
			WithField("path", params.Get("prefix")).
			Error("could not resolve path for prefix")
		o.EncodeError(errors.Codes.ToAPIErr(errors.ErrBadRequest))
		return
	}

	if !prefix.WithPath {
		// list branches then.
		branches, hasMore, err := o.Index.ListBranchesByPrefix(o.Repo.Id, prefix.Ref, maxKeys, params.Get("marker"))
		if err != nil {
			// TODO incorrect error type
			o.Log().WithError(err).Error("could not list branches")
			o.EncodeError(errors.Codes.ToAPIErr(errors.ErrBadRequest))
			return
		}
		// return branch response
		dirs, lastKey := controller.serializeBranches(branches)
		resp := serde.ListBucketResult{
			Name:           o.Repo.Id,
			Prefix:         params.Get("prefix"),
			Delimiter:      delimiter,
			Marker:         params.Get("marker"),
			KeyCount:       len(results),
			MaxKeys:        maxKeys,
			CommonPrefixes: dirs,
			Contents:       make([]serde.Contents, 0),
		}

		if hasMore {
			resp.IsTruncated = true
			if !descend {
				// NextMarker is only set if a delimiter exists
				resp.NextMarker = lastKey
			}
		}

		o.EncodeResponse(resp, http.StatusOK)
		return
	} else {
		prefix, err := path.ResolvePath(params.Get("prefix"))
		if err != nil {
			o.Log().WithError(err).Error("could not list branches")
			o.EncodeError(errors.Codes.ToAPIErr(errors.ErrBadRequest))
			return
		}
		ref = prefix.Ref
		// see if we have a continuation token in the request to pick up from
		var marker path.ResolvedPath
		// strip the branch from the marker
		if len(params.Get("marker")) > 0 {
			marker, err = path.ResolvePath(params.Get("marker"))
			if err != nil || !strings.EqualFold(marker.Ref, prefix.Ref) {
				o.Log().WithError(err).WithFields(logging.Fields{
					"branch": prefix.Ref,
					"path":   prefix.Path,
					"marker": marker,
				}).Error("invalid marker - doesnt start with branch name")
				o.EncodeError(errors.Codes.ToAPIErr(errors.ErrBadRequest))
				return
			}
		}

		results, hasMore, err = o.Index.ListObjectsByPrefix(
			o.Repo.Id,
			prefix.Ref,
			prefix.Path,
			marker.Path,
			maxKeys,
			descend,
		)
		if xerrors.Is(err, db.ErrNotFound) {
			results = make([]*model.Entry, 0) // no results found
		} else if err != nil {
			o.Log().WithError(err).WithFields(logging.Fields{
				"branch": prefix.Ref,
				"path":   prefix.Path,
			}).Error("could not list objects in path")
			o.EncodeError(errors.Codes.ToAPIErr(errors.ErrBadRequest))
			return
		}
	}

	// build a response
	dirs, files, lastKey := controller.serializeEntries(ref, results)
	resp := serde.ListBucketResult{
		Name:           o.Repo.Id,
		Prefix:         params.Get("prefix"),
		Delimiter:      delimiter,
		Marker:         params.Get("marker"),
		KeyCount:       len(results),
		MaxKeys:        maxKeys,
		CommonPrefixes: dirs,
		Contents:       files,
	}

	if hasMore {
		resp.IsTruncated = true
		if !descend {
			// NextMarker is only set if a delimiter exists
			resp.NextMarker = path.WithRef(lastKey, ref)
		}
	}

	o.EncodeResponse(resp, http.StatusOK)
}

func (controller *ListObjects) Handle(o *RepoOperation) {
	o.Incr("list_objects")
	// parse request parameters
	// GET /example?list-type=2&prefix=master%2F&delimiter=%2F&encoding-type=url HTTP/1.1

	// handle GET /?versioning
	keys := o.Request.URL.Query()
	for k := range keys {
		if strings.EqualFold(k, "versioning") {
			// this is a versioning request
			o.EncodeXMLBytes([]byte(serde.VersioningResponse), http.StatusOK)
			return
		}
	}

	// handle ListObjects versions
	listType := o.Request.URL.Query().Get("list-type")
	if strings.EqualFold(listType, "2") {
		controller.ListV2(o)
	} else if strings.EqualFold(listType, "1") {
		controller.ListV1(o)
	} else if len(listType) > 0 {
		o.Log().WithField("list-type", listType).Error("listObjects version not supported")
		o.EncodeError(errors.Codes.ToAPIErr(errors.ErrBadRequest))
		return
	} else {
		// otherwise, handle ListObjectsV1
		controller.ListV1(o)
	}

}
