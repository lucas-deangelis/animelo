package main

import (
	"compress/gzip"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"math"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	_ "github.com/mattn/go-sqlite3"
)

// Define XML structure
type MyAnimeList struct {
	XMLName xml.Name `xml:"myanimelist"`
	Animes  []Anime  `xml:"anime"`
}

type Anime struct {
	SeriesTitle string `xml:"series_title"`
	Status      string `xml:"my_status"`
	AnidbID     int    `xml:"series_animedb_id"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: animelo import mal <file.xml.gz>")
		fmt.Println("Usage: animelo elo")
		os.Exit(1)
	}

	// if os.Args[1] != "elo" || (os.Args[2] != "import" && os.Args[3] != "mal") {
	// 	fmt.Println("Usage: animelo import mal <file.xml.gz>")
	// 	fmt.Println("Usage: animelo elo [database]")
	// 	os.Exit(1)
	// }

	if os.Args[1] == "elo" {
		p := tea.NewProgram(initialModel())
		if err := p.Start(); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		importMAL()
	}
}

func importMAL() {
	filePath := os.Args[3]

	// Open gzipped file
	gzFile, err := os.Open(filePath)
	if err != nil {
		fmt.Printf("Failed to open file: %s\n", err)
		os.Exit(1)
	}
	defer gzFile.Close()

	// Create gzip reader
	gzReader, err := gzip.NewReader(gzFile)
	if err != nil {
		fmt.Printf("Failed to create gzip reader: %s\n", err)
		os.Exit(1)
	}
	defer gzReader.Close()

	fmt.Printf("Decompressing %s...\n", filePath)

	// Read decompressed data
	xmlData, err := ioutil.ReadAll(gzReader)
	if err != nil {
		fmt.Printf("Failed to read decompressed data: %s\n", err)
		os.Exit(1)
	}

	// Parse XML
	var mal MyAnimeList
	if err := xml.Unmarshal(xmlData, &mal); err != nil {
		fmt.Printf("Failed to parse XML: %s\n", err)
		os.Exit(1)
	}

	fmt.Printf("Inserting %d titles into the database...\n", len(mal.Animes))

	err = insertTitlesIntoDB(mal.Animes, "animes.db")
	if err != nil {
		fmt.Printf("Failed to insert titles into the database: %s\n", err)
		os.Exit(1)
	}
}

// insertTitlesIntoDB creates a SQLite database (if it doesn't exist), creates a table,
// and inserts titles with a default elo of 1500.
func insertTitlesIntoDB(series []Anime, dbPath string) error {
	// Open the SQLite database, creating it if it doesn't exist
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	// Create a table if it doesn't exist
	createTableSQL := `CREATE TABLE IF NOT EXISTS animes (
        "id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
		"anidb_id" INTEGER NOT NULL UNIQUE,
        "title" TEXT,
		"status" TEXT,
        "elo" INTEGER,
		"fights" INTEGER DEFAULT 0
    );`
	if _, err := db.Exec(createTableSQL); err != nil {
		return err
	}

	// Prepare insert statement
	insertSQL := `INSERT INTO animes(anidb_id, title, status, elo) VALUES (?, ?, ?, 1500)`
	stmt, err := db.Prepare(insertSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()

	fmt.Printf("Inserting %d titles into the database...\n", len(series))

	// Insert titles into the table
	for _, anime := range series {
		_, err := stmt.Exec(anime.AnidbID, anime.SeriesTitle, anime.Status)
		if err != nil {
			return err
		}
	}

	return nil
}

type anime struct {
	ID      int
	AnidbID int
	Title   string
	Status  string
	Elo     int
	Fights  int
}

type fighting struct {
	up   anime
	down anime
}

type model struct {
	cursor string
	fight  fighting
}

func initialModel() model {
	ani1, ani2 := getTwoRandomAnimes()
	return model{
		cursor: "up",
		fight: fighting{
			up:   ani1,
			down: ani2,
		},
	}
}

func getTwoRandomAnimes() (anime, anime) {
	db, err := sql.Open("sqlite3", "animes.db")
	if err != nil {
		fmt.Printf("Failed to open database: %s\n", err)
		os.Exit(1)
	}
	defer db.Close()

	query := "SELECT * FROM animes WHERE status = 'Completed' ORDER BY fights ASC, RANDOM() LIMIT 2"
	rows, err := db.Query(query)
	if err != nil {
		fmt.Printf("Failed to execute query: %s\n", err)
		os.Exit(1)
	}
	defer rows.Close()

	var animes []anime
	for rows.Next() {
		var a anime
		err := rows.Scan(&a.ID, &a.AnidbID, &a.Title, &a.Status, &a.Elo, &a.Fights)
		if err != nil {
			fmt.Printf("Failed to scan row: %s\n", err)
			os.Exit(1)
		}
		animes = append(animes, a)
	}

	if len(animes) != 2 {
		fmt.Println("Failed to get two animes")
		os.Exit(1)
	}

	return animes[0], animes[1]
}

func updateEloInDB(winner anime, loser anime) {
	db, err := sql.Open("sqlite3", "animes.db")
	if err != nil {
		fmt.Printf("Failed to open database: %s\n", err)
		os.Exit(1)
	}
	defer db.Close()

	winner, loser = updateElo(winner, loser)

	// Update Elo in the database
	updateWinnerQuery := "UPDATE animes SET elo = ?, fights = fights + 1 WHERE anidb_id = ?"
	_, err = db.Exec(updateWinnerQuery, winner.Elo, winner.AnidbID)
	if err != nil {
		fmt.Printf("Failed to update winner's Elo: %s\n", err)
		os.Exit(1)
	}

	updateLoserQuery := "UPDATE animes SET elo = ?, fights = fights + 1 WHERE anidb_id = ?"
	_, err = db.Exec(updateLoserQuery, loser.Elo, loser.AnidbID)
	if err != nil {
		fmt.Printf("Failed to update loser's Elo: %s\n", err)
		os.Exit(1)
	}
}

func updateElo(winner, loser anime) (anime, anime) {
	// Calculate Elo changes
	k := 50
	expectedWinner := 1 / (1 + math.Pow(10, float64(loser.Elo-winner.Elo)/400))
	expectedLoser := 1 - expectedWinner
	eloChangeWinner := int(float64(k) * (1 - expectedWinner))
	eloChangeLoser := int(float64(k) * (0 - expectedLoser))

	// Update Elo
	winner.Elo += eloChangeWinner
	loser.Elo += eloChangeLoser

	return winner, loser
}

func (m model) Init() tea.Cmd {
	return nil
}

func update(cursor string, fight fighting) (tea.Model, tea.Cmd) {
	if cursor == "up" {
		updateEloInDB(fight.up, fight.down)
	} else {
		updateEloInDB(fight.down, fight.up)
	}

	ani1, ani2 := getTwoRandomAnimes()
	return model{
		cursor: "up",
		fight: fighting{
			up:   ani1,
			down: ani2,
		},
	}, nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "k":
			m.cursor = "up"
		case "down", "j":
			m.cursor = "down"
		case "left", "h":
			m.cursor = "up"
			return update(m.cursor, m.fight)
		case "right", "l":
			m.cursor = "down"
			return update(m.cursor, m.fight)
		case "space", "enter":
			return update(m.cursor, m.fight)
		}
	}

	return m, nil
}

func (m model) View() string {
	// Define how each item should be displayed
	firstItem := m.fight.up.Title
	secondItem := m.fight.down.Title

	// highlightStart := "\033[44m" // Set background to blue
	// reset := "\033[0m"           // Reset to default terminal formatting

	// Update the display based on the current selection
	// if m.cursor == "up" {
	// 	firstItem = highlightStart + m.fight.up.Title + reset // Highlight the first item
	// } else {
	// 	secondItem = highlightStart + m.fight.down.Title + reset // Highlight the second item
	// }

	// Render the view with items side by side
	return fmt.Sprintf("%s\n\n%s\nPress q to quit.", firstItem, secondItem)
}
