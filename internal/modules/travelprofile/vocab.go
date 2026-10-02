package travelprofile

// Option is one allowed value of a closed vocabulary with its display label.
// Values must stay in sync with the CHECK constraints in
// migrations/00002_travel_profile.sql; repository tests enforce this.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

var (
	travelStyleOptions = []Option{
		{"backpacker", "Backpacker"},
		{"comfort", "Comfort"},
		{"luxury", "Luxury"},
		{"adventure", "Adventure"},
		{"slow", "Slow travel"},
		{"offbeat", "Offbeat explorer"},
		{"workation", "Workation"},
	}
	budgetBandOptions = []Option{
		{"shoestring", "Shoestring (under ₹1,500/day)"},
		{"budget", "Budget (₹1,500–3,000/day)"},
		{"mid_range", "Mid-range (₹3,000–7,000/day)"},
		{"premium", "Premium (₹7,000–15,000/day)"},
		{"luxury", "Luxury (₹15,000+/day)"},
	}
	groupSizeOptions = []Option{
		{"small", "Small (2–4)"},
		{"medium", "Medium (5–8)"},
		{"large", "Large (9+)"},
		{"any", "No preference"},
	}
	paceOptions = []Option{
		{"relaxed", "Relaxed"},
		{"balanced", "Balanced"},
		{"packed", "Packed itinerary"},
	}
	habitOptions = []Option{
		{"never", "Never"},
		{"sometimes", "Sometimes"},
		{"regularly", "Regularly"},
	}
	dietOptions = []Option{
		{"no_preference", "No preference"},
		{"vegetarian", "Vegetarian"},
		{"eggetarian", "Eggetarian"},
		{"vegan", "Vegan"},
		{"jain", "Jain"},
		{"non_vegetarian", "Non-vegetarian"},
	}
	// ISO 639-1 codes; Indian languages first.
	languageOptions = []Option{
		{"en", "English"}, {"hi", "Hindi"}, {"bn", "Bengali"}, {"te", "Telugu"},
		{"mr", "Marathi"}, {"ta", "Tamil"}, {"ur", "Urdu"}, {"gu", "Gujarati"},
		{"kn", "Kannada"}, {"ml", "Malayalam"}, {"or", "Odia"}, {"pa", "Punjabi"},
		{"as", "Assamese"}, {"ne", "Nepali"}, {"sd", "Sindhi"}, {"ks", "Kashmiri"},
		{"sa", "Sanskrit"}, {"fr", "French"}, {"de", "German"}, {"es", "Spanish"},
		{"it", "Italian"}, {"pt", "Portuguese"}, {"nl", "Dutch"}, {"ru", "Russian"},
		{"ar", "Arabic"}, {"he", "Hebrew"}, {"ja", "Japanese"}, {"ko", "Korean"},
		{"zh", "Chinese"}, {"th", "Thai"}, {"id", "Indonesian"},
	}
)

func valueSet(opts []Option) map[string]bool {
	m := make(map[string]bool, len(opts))
	for _, o := range opts {
		m[o.Value] = true
	}
	return m
}

var (
	travelStyles = valueSet(travelStyleOptions)
	budgetBands  = valueSet(budgetBandOptions)
	groupSizes   = valueSet(groupSizeOptions)
	paces        = valueSet(paceOptions)
	habits       = valueSet(habitOptions)
	diets        = valueSet(dietOptions)
	languages    = valueSet(languageOptions)
)
