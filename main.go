package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"math/rand"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

type App struct {
	Bot          *tgbotapi.BotAPI
	QuestionMgr  *QuestionManager
	UpdateConfig tgbotapi.UpdateConfig
	Users        map[int64]*UserState
	mu           sync.RWMutex
}

func NewApp(botToken string, qm *QuestionManager) (*App, error) {
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return nil, fmt.Errorf("ошибка инициализации бота: %w", err)
	}

	// Включение режима отладки (можно отключить в продакшене)
	bot.Debug = true

	log.Printf("Авторизовался как %s", bot.Self.UserName)

	// Настройка обновлений
	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 60

	return &App{
		Bot:          bot,
		QuestionMgr:  qm,
		UpdateConfig: updateConfig,
		Users:        make(map[int64]*UserState), // Инициализация мапы Users
	}, nil
}

// Структура вопроса
type Question struct {
	Id            int               `json:"id"`
	Question      string            `json:"question"`
	Options       map[string]string `json:"options"`
	CorrectAnswer []string          `json:"correct_answer"`
}

type QuestionManager struct {
	Themes map[string][]Question
}

func NewQuestionManager() *QuestionManager {
	return &QuestionManager{
		Themes: make(map[string][]Question),
	}
}

// Структура состояния пользователя
type UserState struct {
	CurrentQuestion int
	Score           int
	QuestionOrder   []int
	SelectedTheme   string
	SetupStep       string
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Cant load .env")
	}

	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN не задан")
	}

	qm := NewQuestionManager()

	dirPath := "./quizzes"

	if err := qm.LoadAllQuestionsFromDir(dirPath); err != nil {
		log.Fatalf("Ошибка загрузки вопросов: %v", err)
	}

	log.Printf("Загружено %d тем", len(qm.Themes))

	app, err := NewApp(botToken, qm)
	if err != nil {
		log.Fatalf("Ошибка инициализации приложения: %v", err)
	}

	app.Start()
}

func (app *App) Start() {
	updates := app.Bot.GetUpdatesChan(app.UpdateConfig)

	for update := range updates {
		if update.Message != nil {
			if update.Message.IsCommand() {
				switch update.Message.Command() {
				case "start":
					app.handleStartCommand(update.Message)
				case "help":
					app.handleHelpCommand(update.Message)
				case "restart":
					app.handleRestartCommand(update.Message)
				default:
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Неизвестная команда. Используйте /help для списка доступных команд.")
					app.Bot.Send(msg)
				}
			}
		}

		if update.CallbackQuery != nil {
			app.handleCallbackQuery(update.CallbackQuery)
		}
	}
}

func (qm *QuestionManager) loadQuestions(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("ошибка при чтении файла: %w", err)
	}

	var qs []Question = make([]Question, 0, 100)

	if err := json.Unmarshal(data, &qs); err != nil {
		return fmt.Errorf("ошибка при разборе JSON: %w", err)
	}

	qm.Themes[filename] = qs

	return nil
}

func (qm *QuestionManager) LoadAllQuestionsFromDir(dirPath string) error {
	// Проверка, что директория существует
	info, err := os.Stat(dirPath)
	if err != nil {
		return fmt.Errorf("ошибка при доступе к директории %s: %w", dirPath, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("%s не является директорией", dirPath)
	}

	// Чтение списка файлов в директории
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Errorf("ошибка при чтении директории %s: %w", dirPath, err)
	}

	// Перебор файлов и загрузка JSON файлов
	for _, file := range files {
		if file.IsDir() {
			continue // Пропуск поддиректорий, если необходимо
		}

		if strings.ToLower(filepath.Ext(file.Name())) != ".json" {
			continue // Пропуск файлов с другими расширениями
		}

		filePath := filepath.Join(dirPath, file.Name())
		if err := qm.loadQuestions(filePath); err != nil {
			log.Printf("Предупреждение: %v", err)
			continue
		}

		log.Printf("Загружена тема: %s из файла %s", strings.TrimSuffix(file.Name(), ".json"), file.Name())
	}

	return nil
}

// Универсальная функция извлечения буквы из строки
func extractLetter(text string) string {
	// Предполагается, что текст начинается с "A. ..." или "A) ..."
	separators := []string{".", ")", ":"}
	for _, sep := range separators {
		parts := strings.SplitN(text, sep, 2)
		if len(parts) >= 2 {
			letter := strings.TrimSpace(parts[0])
			return strings.ToUpper(letter)
		}
	}
	// Если разделитель не найден, попробуем взять первый символ
	if len(text) > 0 {
		return strings.ToUpper(string(text[0]))
	}
	return ""
}

func (app *App) handleStartCommand(message *tgbotapi.Message) {
	// Захват мьютекса для безопасного доступа к Users

	// Создание или обновление состояния пользователя
	app.mu.Lock()
	app.Users[message.From.ID] = &UserState{
		SetupStep: "select_theme",
	}
	app.mu.Unlock()

	// Собираем все темы
	var themes []string
	for theme := range app.QuestionMgr.Themes {
		themes = append(themes, theme)
	}

	// Проверка наличия тем
	if len(themes) == 0 {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Темы не найдены. Пожалуйста, попробуйте позже.")
		if _, err := app.Bot.Send(msg); err != nil {
			log.Printf("Ошибка при отправке сообщения: %v", err)
		}
		return
	}

	// Создание инлайн-клавиатуры с темами
	var buttons [][]tgbotapi.InlineKeyboardButton
	for _, theme := range themes {
		button := tgbotapi.NewInlineKeyboardButtonData(theme, fmt.Sprintf("theme_%s", theme))
		buttons = append(buttons, tgbotapi.NewInlineKeyboardRow(button))
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(buttons...)

	// Отправка сообщения с выбором темы
	msg := tgbotapi.NewMessage(message.Chat.ID, "Выберите тему для викторины:")
	msg.ReplyMarkup = keyboard
	if _, err := app.Bot.Send(msg); err != nil {
		log.Printf("Ошибка при отправке сообщения: %v", err)
	}
}

// Обработка команды /restart
func (app *App) handleRestartCommand(message *tgbotapi.Message) {
	// Собираем все вопросы из всех тем
	var allQuestions []Question
	for _, questions := range app.QuestionMgr.Themes {
		allQuestions = append(allQuestions, questions...)
	}

	// Проверка наличия вопросов
	if len(allQuestions) == 0 {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Вопросы не найдены. Пожалуйста, попробуйте позже.")
		if _, err := app.Bot.Send(msg); err != nil {
			log.Printf("Ошибка при отправке сообщения: %v", err)
		}
		return
	}

	// Инициализация порядка вопросов в исходном порядке
	order := make([]int, len(allQuestions))
	for i := range order {
		order[i] = i
	}

	// Обновляем состояние пользователя
	app.mu.Lock()
	app.Users[message.From.ID] = &UserState{
		CurrentQuestion: 0,
		Score:           0,
		QuestionOrder:   order,
		SelectedTheme:   app.Users[message.From.ID].SelectedTheme,
	}
	app.mu.Unlock()

	log.Printf("Пользователь %s перезапустил викторину", message.From.UserName)

	// Отправляем сообщение о перезапуске викторины
	msg := tgbotapi.NewMessage(message.Chat.ID, "Викторина перезапущена.")
	if _, err := app.Bot.Send(msg); err != nil {
		log.Printf("Ошибка при отправке сообщения: %v", err)
	}

	// Отправляем первый вопрос
	app.sendQuestion(message.Chat.ID, message.From.ID)
}

// Обработка команды /help
func (app *App) handleHelpCommand(message *tgbotapi.Message) {
	helpText := "Доступные команды:\n" +
		"/start - Начать викторину в стандартном порядке\n" +
		"/random - Начать викторину в случайном порядке\n" +
		"/restart - Перезапустить викторину\n" +
		"/help - Показать список команд"
	msg := tgbotapi.NewMessage(message.Chat.ID, helpText)
	if _, err := app.Bot.Send(msg); err != nil {
		log.Printf("Ошибка при отправке сообщения: %v", err)
	}
}

// Функция отправки вопроса
func (app *App) sendQuestion(chatID int64, userID int64) {
	// Получаем состояние пользователя
	app.mu.RLock()
	state, exists := app.Users[userID]
	app.mu.RUnlock()

	if !exists {
		msg := tgbotapi.NewMessage(chatID, "Пожалуйста, начните викторину с помощью команды /start или /random.")
		if _, err := app.Bot.Send(msg); err != nil {
			log.Printf("Ошибка при отправке сообщения: %v", err)
		}
		return
	}

	questions, themeExists := app.QuestionMgr.Themes[state.SelectedTheme]

	if !themeExists || len(questions) == 0 {
		msg := tgbotapi.NewMessage(chatID, "Выбранная тема недоступна. Пожалуйста, выберите другую тему с помощью /start.")
		if _, err := app.Bot.Send(msg); err != nil {
			log.Printf("Ошибка при отправке сообщения: %v", err)
		}
		return
	}

	// Проверяем, завершена ли викторина
	if state.CurrentQuestion >= len(questions) {
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Викторина завершена! Ваш счёт: %d/%d", state.Score, len(questions)))
		if _, err := app.Bot.Send(msg); err != nil {
			log.Printf("Ошибка при отправке сообщения: %v", err)
		}

		// Удаляем состояние пользователя
		app.mu.Lock()
		delete(app.Users, userID)
		app.mu.Unlock()

		return
	}

	// Получаем текущий вопрос
	currentIndex := state.QuestionOrder[state.CurrentQuestion]
	if currentIndex >= len(questions) {
		msg := tgbotapi.NewMessage(chatID, "Ошибка: неправильный индекс вопроса.")
		if _, err := app.Bot.Send(msg); err != nil {
			log.Printf("Ошибка при отправке сообщения: %v", err)
		}
		return
	}

	currentQuestion := questions[currentIndex]
	questionText := "*" + currentQuestion.Question + "*\n\n"

	// Создаем кнопки для вариантов ответа, используя буквы из опций
	var buttons []tgbotapi.InlineKeyboardButton
	for key, option := range currentQuestion.Options {
		button := tgbotapi.NewInlineKeyboardButtonData(key, key)
		buttons = append(buttons, button)
		questionText += fmt.Sprintf("%s. %s\n", key, option)
	}

	// Создаём инлайн-клавиатуру
	keyboard := tgbotapi.NewInlineKeyboardMarkup(buttons)

	// Формируем сообщение
	msg := tgbotapi.NewMessage(chatID, questionText)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	// Отправляем сообщение
	if _, err := app.Bot.Send(msg); err != nil {
		log.Printf("Ошибка при отправке вопроса: %v", err)
	}
}

func (app *App) handleCallbackQuery(callback *tgbotapi.CallbackQuery) {
	data := callback.Data
	userID := callback.From.ID
	chatID := callback.Message.Chat.ID

	defer app.answerCallback(callback.ID)

	app.mu.Lock()
	state, exists := app.Users[userID]
	if !exists {
		app.mu.Unlock()
		msg := tgbotapi.NewMessage(chatID, "Пожалуйста, начните викторину с помощью команды /start.")
		app.Bot.Send(msg)
		return
	}

	switch state.SetupStep {
	case "select_theme":
		if strings.HasPrefix(data, "theme_") {
			theme := strings.TrimPrefix(data, "theme_")
			state.SelectedTheme = theme
			state.SetupStep = "select_order"
			app.Users[userID] = state
			app.mu.Unlock()

			// Создание инлайн-клавиатуры с выбором порядка вопросов
			buttons := [][]tgbotapi.InlineKeyboardButton{
				{tgbotapi.NewInlineKeyboardButtonData("Упорядоченный", "order_ordered")},
				{tgbotapi.NewInlineKeyboardButtonData("Случайный", "order_random")},
			}
			keyboard := tgbotapi.NewInlineKeyboardMarkup(buttons...)

			// Отправка сообщения с выбором порядка вопросов
			msg := tgbotapi.NewMessage(chatID, "Выберите порядок вопросов:")
			msg.ReplyMarkup = keyboard
			if _, err := app.Bot.Send(msg); err != nil {
				log.Printf("Ошибка при отправке сообщения: %v", err)
			}
		} else {
			app.mu.Unlock()
		}
	case "select_order":
		if data == "order_ordered" || data == "order_random" {
			// Определяем порядок вопросов
			var order []int
			questions, themeExists := app.QuestionMgr.Themes[state.SelectedTheme]
			if !themeExists || len(questions) == 0 {
				app.mu.Unlock()
				msg := tgbotapi.NewMessage(chatID, "Выбранная тема недоступна. Пожалуйста, начните викторину снова с помощью /start.")
				app.Bot.Send(msg)
				return
			}
			order = make([]int, len(questions))
			for i := range order {
				order[i] = i
			}
			if data == "order_random" {
				rand.Shuffle(len(order), func(i, j int) {
					order[i], order[j] = order[j], order[i]
				})
			}

			// Обновляем состояние пользователя
			state.CurrentQuestion = 0
			state.Score = 0
			state.QuestionOrder = order
			state.SetupStep = ""
			app.Users[userID] = state
			app.mu.Unlock()

			log.Printf("Пользователь %s начал викторину по теме: %s в порядке: %s", callback.From.UserName, state.SelectedTheme, data)

			// Отправка первого вопроса
			app.sendQuestion(chatID, userID)
		} else {
			app.mu.Unlock()
		}
	default:
		selectedLetter := strings.ToUpper(strings.TrimSpace(data))
		questions, themeExists := app.QuestionMgr.Themes[state.SelectedTheme]
		if !themeExists || len(questions) == 0 {
			app.mu.Unlock()
			msg := tgbotapi.NewMessage(chatID, "Выбранная тема недоступна. Пожалуйста, выберите другую тему с помощью /start.")
			app.Bot.Send(msg)
			return
		}
		if state.CurrentQuestion >= len(questions) {
			app.mu.Unlock()
			msg := tgbotapi.NewMessage(chatID, "Викторина уже завершена. Начните заново с помощью /start или /random.")
			app.Bot.Send(msg)
			return
		}
		currentQuestion := questions[state.QuestionOrder[state.CurrentQuestion]]
		// Проверка ответа
		var response string
		correctOption := ""
		if selectedLetter == "" {
			response = "Некорректный выбор. Пожалуйста, используйте предоставленные кнопки."
		} else if contains(currentQuestion.CorrectAnswer, selectedLetter) {
			state.Score++
			response = "Правильно! 👍\n"
		} else {
			response = "Неправильно. ❌\n"
			// Поиск полного текста правильного ответа
			correctOption = currentQuestion.CorrectAnswer[0] // Предполагаем, что только один правильный ответ
			correctText := currentQuestion.Options[correctOption]
			response += fmt.Sprintf("Правильный ответ: %s: %s\n", correctOption, correctText)
		}
		response += "Ваш текущий счёт: " + strconv.Itoa(state.Score) + "/" + strconv.Itoa(len(questions))
		state.CurrentQuestion++
		app.Users[userID] = state
		app.mu.Unlock()

		resultMsg := tgbotapi.NewMessage(chatID, response)
		if _, err := app.Bot.Send(resultMsg); err != nil {
			log.Printf("Ошибка при отправке результата: %v", err)
		}

		// Отправка следующего вопроса или завершение викторины
		if state.CurrentQuestion < len(questions) {
			app.sendQuestion(chatID, userID)
		} else {
			// Завершение викторины
			finalMsg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Поздравляем! Вы завершили викторину.\nВаш итоговый счёт: %d/%d", state.Score, len(questions)))
			if _, err := app.Bot.Send(finalMsg); err != nil {
				log.Printf("Ошибка при отправке итогового сообщения: %v", err)
			}

			// Удаляем состояние пользователя
			app.mu.Lock()
			delete(app.Users, userID)
			app.mu.Unlock()
		}
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, item) {
			return true
		}
	}
	return false
}

func (app *App) answerCallback(callbackID string) {
	answer := tgbotapi.NewCallback(callbackID, "")
	if _, err := app.Bot.Request(answer); err != nil {
		log.Printf("Ошибка при ответе на CallbackQuery: %v", err)
	}
}
