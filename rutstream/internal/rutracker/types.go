package rutracker

// Topic — раздача (тема форума).
type Topic struct {
	ID       int64  `json:"id"`
	ForumID  int    `json:"forumId"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Author   string `json:"author"`
	SizeText string `json:"sizeText"`
	Size     int64  `json:"size"`
	Seeds    int    `json:"seeds"`
	Peers    int    `json:"peers"`
	Rating   float64 `json:"rating"`
	Downloads int   `json:"downloads"`
	Date     string `json:"date"`
	Checked  bool   `json:"checked"`
	Magnet   string `json:"magnet"`
	InfoHash string `json:"infoHash"`
	Trackers []string `json:"trackers"`

	// Метаданные из тела темы.
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Year     string `json:"year"`
	Genre    string `json:"genre"`
	Format   string `json:"format"`
	Bitrate  string `json:"bitrate"`
	Duration string `json:"duration"`
	Quality  string `json:"quality"`
	// ForumIDs — разделы, в которых находится тема (из хлебных крошек
	// страницы). Нужны для рекомендаций «похожие исполнители».
	ForumIDs []int `json:"forumIds"`

	// Треклист и список файлов (заполняются при разборе темы).
	Tracks []SiteTrack `json:"tracks"`
	Files  []TopicFile `json:"files"`
}

// SiteTrack — трек из треклиста темы.
type SiteTrack struct {
	Pos      int     `json:"pos"`
	Disc     int     `json:"disc"`
	Title    string  `json:"title"`
	Duration float64 `json:"duration"`
}

// TopicFile — файл внутри раздачи (по списку rutracker).
type TopicFile struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SizeText string `json:"sizeText"`
}
