package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Структура вопроса
type Question struct {
	Question      string   `json:"question"`
	Options       []string `json:"options"`
	CorrectOption string   `json:"correct_answer"` // Только буква: "A", "B", "C" и т.д.
}

// Структура состояния пользователя
type UserState struct {
	CurrentQuestion int
	Score           int
	QuestionOrder   []int // Порядок вопросов для пользователя
}

// Глобальные переменные
var (
	questions  []Question
	userStates = make(map[int64]*UserState)
	mu         sync.Mutex
)

func main() {
	// Инициализация генератора случайных чисел
	rand.Seed(time.Now().UnixNano())

	// Получение токена из переменной окружения
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN не задан")
	}

	// Загрузка вопросов из файла
	err := loadQuestions("questions.json")
	if err != nil {
		log.Fatalf("Ошибка загрузки вопросов: %v", err)
	}

	log.Printf("Загружено %d вопросов", len(questions))

	// Инициализация бота
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panic(err)
	}

	// Включение режима отладки (можно отключить в продакшене)
	bot.Debug = true

	log.Printf("Авторизовался как %s", bot.Self.UserName)

	// Настройка обновлений
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	// Обработка обновлений
	for update := range updates {
		// Обработка сообщений
		if update.Message != nil {
			if update.Message.IsCommand() {
				switch update.Message.Command() {
				case "start":
					handleStartCommand(bot, update.Message)
				case "help":
					handleHelpCommand(bot, update.Message)
				case "restart":
					handleRestartCommand(bot, update.Message)
				case "random":
					handleRandomCommand(bot, update.Message)
				default:
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Неизвестная команда. Используйте /help для списка доступных команд.")
					if _, err := bot.Send(msg); err != nil {
						log.Printf("Ошибка при отправке сообщения: %v", err)
					}
				}
			}
		}

		// Обработка ответов через инлайн-кнопки
		if update.CallbackQuery != nil {
			handleCallbackQuery(bot, update.CallbackQuery)
		}
	}
}

// Функция загрузки вопросов из файла
func loadQuestions(filename string) error {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &questions); err != nil {
		return err
	}
	// Преобразование CorrectOption в верхний регистр и удаление пробелов
	for i := range questions {
		q := &questions[i]
		q.CorrectOption = extractLetter(strings.ToUpper(strings.TrimSpace(q.CorrectOption)))
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

// Обработка команды /start
func handleStartCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	mu.Lock()

	// Инициализация порядка вопросов в исходном порядке
	order := make([]int, len(questions))
	for i := range order {
		order[i] = i
	}

	userStates[message.From.ID] = &UserState{
		CurrentQuestion: 0,
		Score:           0,
		QuestionOrder:   order,
	}

	mu.Unlock() // Освобождаем мьютекс перед вызовом sendQuestion

	log.Printf("Пользователь %s начал викторину", message.From.UserName)

	sendQuestion(bot, message.Chat.ID, message.From.ID)
}

// Обработка команды /restart
func handleRestartCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	mu.Lock()

	// Инициализация порядка вопросов в исходном порядке
	order := make([]int, len(questions))
	for i := range order {
		order[i] = i
	}

	userStates[message.From.ID] = &UserState{
		CurrentQuestion: 0,
		Score:           0,
		QuestionOrder:   order,
	}

	mu.Unlock() // Освобождаем мьютекс перед дальнейшими действиями

	log.Printf("Пользователь %s перезапустил викторину", message.From.UserName)

	msg := tgbotapi.NewMessage(message.Chat.ID, "Викторина перезапущена.")
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Ошибка при отправке сообщения: %v", err)
	}

	sendQuestion(bot, message.Chat.ID, message.From.ID)
}

// Обработка команды /help
func handleHelpCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	helpText := "Доступные команды:\n" +
		"/start - Начать викторину в стандартном порядке\n" +
		"/random - Начать викторину в случайном порядке\n" +
		"/restart - Перезапустить викторину\n" +
		"/help - Показать список команд"
	msg := tgbotapi.NewMessage(message.Chat.ID, helpText)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Ошибка при отправке сообщения: %v", err)
	}
}

// Обработка команды /random
func handleRandomCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	mu.Lock()

	// Инициализация порядка вопросов в случайном порядке
	order := make([]int, len(questions))
	for i := range order {
		order[i] = i
	}
	rand.Shuffle(len(order), func(i, j int) {
		order[i], order[j] = order[j], order[i]
	})

	userStates[message.From.ID] = &UserState{
		CurrentQuestion: 0,
		Score:           0,
		QuestionOrder:   order,
	}

	mu.Unlock() // Освобождаем мьютекс перед вызовом sendQuestion

	log.Printf("Пользователь %s начал викторину в случайном порядке", message.From.UserName)

	sendQuestion(bot, message.Chat.ID, message.From.ID)
}

// Функция отправки вопроса
func sendQuestion(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	mu.Lock()
	state, exists := userStates[userID]
	mu.Unlock()

	if !exists {
		msg := tgbotapi.NewMessage(chatID, "Пожалуйста, начните викторину с помощью команды /start или /random.")
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Ошибка при отправке сообщения: %v", err)
		}
		return
	}

	if state.CurrentQuestion >= len(questions) {
		msg := tgbotapi.NewMessage(chatID, "Викторина завершена! Ваш счёт: "+strconv.Itoa(state.Score)+"/"+strconv.Itoa(len(questions)))
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Ошибка при отправке сообщения: %v", err)
		}
		mu.Lock()
		delete(userStates, userID)
		mu.Unlock()
		return
	}

	currentQuestion := questions[state.QuestionOrder[state.CurrentQuestion]]
	questionText := "*" + currentQuestion.Question + "*\n\n"

	// Создаем кнопки для вариантов ответа, используя буквы из опций
	var buttons []tgbotapi.InlineKeyboardButton
	for _, option := range currentQuestion.Options {
		letter := extractLetter(option)
		if letter == "" {
			log.Printf("Некорректная опция без буквы: %s", option)
			continue
		}
		button := tgbotapi.NewInlineKeyboardButtonData(letter, letter)
		buttons = append(buttons, button)
		questionText += option + "\n"
	}

	// Создаём инлайн-клавиатуру
	keyboard := tgbotapi.NewInlineKeyboardMarkup(buttons)

	msg := tgbotapi.NewMessage(chatID, questionText)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	if _, err := bot.Send(msg); err != nil {
		log.Printf("Ошибка при отправке вопроса: %v", err)
	}
}

// Функция обработки ответов через инлайн-кнопки
func handleCallbackQuery(bot *tgbotapi.BotAPI, callback *tgbotapi.CallbackQuery) {
	userID := callback.From.ID
	selectedLetter := strings.ToUpper(strings.TrimSpace(callback.Data))
	chatID := callback.Message.Chat.ID

	mu.Lock()
	state, exists := userStates[userID]
	mu.Unlock()

	if !exists {
		msg := tgbotapi.NewMessage(chatID, "Пожалуйста, начните викторину с помощью команды /start или /random.")
		bot.Send(msg)
		answerCallback(bot, callback.ID)
		return
	}

	if state.CurrentQuestion >= len(questions) {
		msg := tgbotapi.NewMessage(chatID, "Викторина уже завершена. Начните заново с помощью /start или /random.")
		bot.Send(msg)
		answerCallback(bot, callback.ID)
		return
	}

	currentQuestion := questions[state.QuestionOrder[state.CurrentQuestion]]

	// Проверка ответа
	var response string
	if selectedLetter == currentQuestion.CorrectOption {
		state.Score++
		response = "Правильно! 👍\n"
	} else {
		response = "Неправильно. ❌\n"
		// Поиск полного текста правильного ответа
		correctText := getOptionText(currentQuestion, currentQuestion.CorrectOption)
		response += "Правильный ответ: " + correctText + "\n"
	}

	response += "Ваш текущий счёт: " + strconv.Itoa(state.Score) + "/" + strconv.Itoa(len(questions))

	// Отправка сообщения с результатом
	resultMsg := tgbotapi.NewMessage(chatID, response)
	if _, err := bot.Send(resultMsg); err != nil {
		log.Printf("Ошибка при отправке результата: %v", err)
	}

	// Переход к следующему вопросу
	mu.Lock()
	state.CurrentQuestion++
	userStates[userID] = state
	mu.Unlock()

	// Отправка следующего вопроса или завершение викторины
	if state.CurrentQuestion < len(questions) {
		sendQuestion(bot, chatID, userID)
	} else {
		// Завершение викторины
		finalMsg := tgbotapi.NewMessage(chatID, "Поздравляем! Вы завершили викторину.\nВаш итоговый счёт: "+strconv.Itoa(state.Score)+"/"+strconv.Itoa(len(questions)))
		if _, err := bot.Send(finalMsg); err != nil {
			log.Printf("Ошибка при отправке итогового сообщения: %v", err)
		}
		mu.Lock()
		delete(userStates, userID)
		mu.Unlock()
	}

	// Ответ на CallbackQuery, чтобы убрать "часики" ожидания
	answerCallback(bot, callback.ID)
}

// Вспомогательная функция для получения текста опции по букве
func getOptionText(q Question, letter string) string {
	for _, option := range q.Options {
		optLetter := extractLetter(option)
		if optLetter == letter {
			return option
		}
	}
	return ""
}

// Вспомогательная функция для ответа на CallbackQuery
func answerCallback(bot *tgbotapi.BotAPI, callbackID string) {
	callbackConfig := tgbotapi.NewCallback(callbackID, "")
	if _, err := bot.Request(callbackConfig); err != nil {
		log.Printf("Ошибка при ответе на CallbackQuery: %v", err)
	}
}
