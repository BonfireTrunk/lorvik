-- name: InsertCardSubscription :exec
-- Adds the subscription unless the chat already has one to the card. No unique index
-- backs this, so two concurrent calls may both insert; DeleteCardSubscription removes
-- every match and ListCardSubscriptions groups them.
INSERT INTO card_subscriptions (org_id, card_id, agent_id, session, created_at)
SELECT @org_id::bigint, @card_id::bigint, @agent_id::bigint, @session::text, @created_at::timestamptz
WHERE NOT EXISTS (
    SELECT 1 FROM card_subscriptions w
    WHERE w.card_id = @card_id AND w.agent_id = @agent_id AND w.session = @session);

-- name: DeleteCardSubscription :exec
DELETE FROM card_subscriptions WHERE card_id = @card_id AND agent_id = @agent_id AND session = @session;

-- name: ListCardSubscriptions :many
-- The chat's subscriptions to cards that are not done or dropped, oldest first, one
-- row per card.
SELECT c.id AS card_id, c.project_id, p.slug AS project_slug, c.number, c.title, c.status,
       min(w.created_at)::timestamptz AS subscribed_at
FROM card_subscriptions w
JOIN cards c ON c.id = w.card_id
JOIN projects p ON p.id = c.project_id
WHERE w.agent_id = @agent_id AND w.session = @session AND c.status >= 0
GROUP BY c.id, p.slug
ORDER BY min(w.created_at), c.id;

-- name: SubscribedCardsAmong :many
-- The cards among card_ids that the chat is subscribed to.
SELECT DISTINCT card_id FROM card_subscriptions
WHERE org_id = @org_id AND agent_id = @agent_id AND session = @session AND card_id = ANY(@card_ids::bigint[]);

-- name: DeleteCardSubscriptionsOfLostChats :execrows
-- Deletes the subscriptions of chats not seen since seen_since, or whose chat row is gone.
DELETE FROM card_subscriptions w
WHERE NOT EXISTS (
    SELECT 1 FROM agent_sessions s
    WHERE s.agent_id = w.agent_id AND s.session = w.session AND s.last_seen_at >= @seen_since);

-- name: DeleteCardSubscriptionsOfClosedCards :execrows
-- Deletes the subscriptions on cards done or dropped before closed_before, or gone. A
-- card closed since then keeps them, so the chat's listener still reads the closing event.
DELETE FROM card_subscriptions w
WHERE NOT EXISTS (SELECT 1 FROM cards c WHERE c.id = w.card_id AND (c.status >= 0 OR coalesce(c.closed_at, c.updated_at) >= @closed_before::timestamptz));
