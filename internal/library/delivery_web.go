package library

import (
	"net/http"
	"strconv"
)

func (w *Web) deliveryData() pageData {
	return pageData{DeliveryEnabled: w.Telegram.Enabled, DailyTime: w.Telegram.DailyTime, DeliverySize: w.Telegram.Size, DeliveryChat: w.Telegram.ChatID}
}
func (w *Web) deliveryList(rw http.ResponseWriter, r *http.Request) {
	data := w.deliveryData()
	var err error
	data.Deliveries, err = w.Store.Deliveries()
	if err != nil {
		http.Error(rw, "добірки недоступні", http.StatusServiceUnavailable)
		return
	}
	w.render(rw, "deliveries", data)
}
func (w *Web) deliveryDetail(rw http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	delivery, err := w.Store.Delivery(id)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	data := w.deliveryData()
	data.Delivery = &delivery
	w.render(rw, "delivery", data)
}
func (w *Web) deliveryPreview(rw http.ResponseWriter, r *http.Request) {
	id, err := w.Store.PreviewDelivery(w.Telegram, "manual", "")
	if err != nil {
		http.Error(rw, "Не вдалося підготувати добірку: "+err.Error(), 400)
		return
	}
	http.Redirect(rw, r, "/deliveries/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}
func (w *Web) deliveryQueue(rw http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	action := r.PathValue("action")
	if action != "send" && action != "retry" {
		http.NotFound(rw, r)
		return
	}
	if err = w.Store.QueueDelivery(id, w.Telegram, action == "retry"); err != nil {
		http.Error(rw, "Не вдалося поставити доставку: "+err.Error(), 400)
		return
	}
	http.Redirect(rw, r, "/deliveries/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}
