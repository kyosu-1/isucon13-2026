-- 追加インデックス（init.sh が initialize のたびに流す。init.sql は TRUNCATE なのでインデックスは残る。
-- 既にあると "Duplicate key name" になるので mysql -f で流して無視する）
CREATE INDEX icons_user_id ON icons (user_id);
CREATE INDEX themes_user_id ON themes (user_id);
CREATE INDEX livestreams_user_id ON livestreams (user_id);
CREATE INDEX livestream_tags_livestream_id ON livestream_tags (livestream_id);
CREATE INDEX livestream_tags_tag_id ON livestream_tags (tag_id, livestream_id);
CREATE INDEX livecomments_livestream_id ON livecomments (livestream_id, created_at);
CREATE INDEX reactions_livestream_id ON reactions (livestream_id, created_at);
CREATE INDEX ng_words_user_livestream ON ng_words (user_id, livestream_id);
CREATE INDEX ng_words_livestream_id ON ng_words (livestream_id);
CREATE INDEX lvh_livestream_id ON livestream_viewers_history (livestream_id);
CREATE INDEX lvh_user_livestream ON livestream_viewers_history (user_id, livestream_id);
CREATE INDEX livecomment_reports_livestream_id ON livecomment_reports (livestream_id);
CREATE INDEX reservation_slots_start_end ON reservation_slots (start_at, end_at);
