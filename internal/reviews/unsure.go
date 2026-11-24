package reviews

// A post whose failure leaves unclear whether GitHub took the review (gh timed out or was
// killed, the connection dropped, GitHub answered 5xx) is never pressed again on a guess: a
// review on GitHub cannot be taken back, so a second one would stay. The launch is held as
// unsure, every press of it is refused, and once a poll has read the PR again my newest reviews
// of it are read: one on the draft's commit, submitted after the post was sent, is the post
// GitHub took and is recorded; with none, the post may be pressed again.

const (
	// maxUnsurePosts is the most posts being sent or held unsure at once; a press past it is
	// refused until some are settled.
	maxUnsurePosts = 20
	// clockSkewMs is how much earlier than the post was sent, by Conn's clock, GitHub's clock
	// may date the review it took.
	clockSkewMs = 5 * 60_000
)

// Why a press is refused while posts are unsettled.
const (
	textPostChecking = "Checking whether GitHub took the review: try again after the next poll"
	textPostTooMany  = "too many reviews are being posted or checked: try again after the next poll"
)

// unsurePost is a post whose failure left unclear whether GitHub took the review.
type unsurePost struct {
	// sentAt is when the post was sent, by Conn's clock.
	sentAt float64
	// checking is that a read of my reviews for it is running.
	checking bool
}

// held is whether the draft of launch, on the PR key, is being posted or is unsure: it is
// neither replaced by a newer file nor set aside meanwhile.
func (r *Reviews) held(key string, launch *Launch) bool {
	target := launchID{key: key, at: launch.At}
	_, unsure := r.unsure[target]

	return anyOf(r.posting[target], unsure)
}

// checkUnsure starts the check of each unsure post sent before the poll that read at polledAt,
// whose PR that poll knows, as login (me). A check already running is left to finish, and a
// launch no longer in the file has no draft to post: it is let go.
func (r *Reviews) checkUnsure(polledAt float64, login string) {
	if login == "" {
		return
	}

	for target, unsure := range r.unsure {
		if r.launchOf(target) == nil {
			delete(r.unsure, target)

			continue
		}

		_, known := r.pullOf(target.key)
		if anyOf(unsure.checking, !known, unsure.sentAt >= polledAt) {
			continue
		}

		unsure.checking = true
		r.unsure[target] = unsure

		r.spawn(func() { r.checkPost(target, unsure.sentAt, login) })
	}
}

// checkPost reads my newest reviews of the PR target names, without the mutex, and settles its
// unsure post: the review that is the post is recorded, and with none the post may be pressed
// again. A read that fails leaves it unsure, to be checked after the next poll.
func (r *Reviews) checkPost(target launchID, sentAt float64, login string) {
	parts, _ := ParseKey(target.key)

	var (
		found []PostedReview
		err   error
	)

	// gh's own limit on a call bounds the read.
	r.outside(func() {
		found, err = r.deps.Gh.MyReviews(r.ctx, parts.Repo, parts.Number, login)
	})

	if err != nil {
		r.logf("reviews: whether GitHub took the review of %s is not known %v", target.key, err)

		if unsure, still := r.unsure[target]; still {
			unsure.checking = false
			r.unsure[target] = unsure
		}

		return
	}

	delete(r.unsure, target)

	launch := r.launchOf(target)
	if launch != nil && launch.Draft != nil {
		if review, took := tookPost(found, launch.Draft.Commit, sentAt); took {
			r.markPosted(launch, review.SubmittedAt, review.URL)
		}
	}

	r.bump()
}

// tookPost is the review among mine that is the post sent at sentAt on commit: the newest on
// that commit submitted no earlier than clockSkewMs before it was sent.
func tookPost(mine []PostedReview, commit string, sentAt float64) (PostedReview, bool) {
	var (
		newest PostedReview
		found  bool
	)

	for _, review := range mine {
		if anyOf(review.Commit != commit, review.SubmittedAt < sentAt-clockSkewMs) {
			continue
		}

		if anyOf(!found, review.SubmittedAt > newest.SubmittedAt) {
			newest, found = review, true
		}
	}

	return newest, found
}

// markPosted records that the draft of launch was posted at postedAt, to url ("" when it is
// not known), and saves: the draft is never posted again. Posted wins over a set-aside made
// while the post was out.
func (r *Reviews) markPosted(launch *Launch, postedAt float64, url string) *string {
	var shown *string

	if url != "" {
		shown = &url
	}

	launch.Draft.PostedAt, launch.Draft.URL, launch.Draft.SetAside = &postedAt, shown, nil

	r.save()
	r.bump()

	return shown
}
