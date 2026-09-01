// Package leadgen turns a list of companies into a lead list: it categorizes
// them now, and (from M6) analyses each category's gaps and drafts outreach.
//
// The ordering rule that shapes this package is a cost one. Google already
// answers most of "what kind of business is this?" for free, in the types[]
// every Places row carries; the model tier exists for the residue, in batches,
// once per company per taxonomy version. A region of two hundred companies
// should reach the `claude` CLI with a handful of rows, and a second run with
// none at all.
package leadgen

// Category is a normalized, business-shaped classification — the unit every
// later lead-gen stage groups by. It is deliberately much smaller than
// Google's ~100-value type taxonomy: an operator segments outreach by "beauty"
// or "automotive", not by `hair_care` vs `beauty_salon`.
//
// The set is closed and lives in code (SD-1). It is also the vocabulary handed
// to the model, which is what makes the model tier safe: an answer outside this
// list is discarded, never stored.
type Category string

const (
	CategoryRestaurant           Category = "restaurant"
	CategoryRetail               Category = "retail"
	CategoryBeauty               Category = "beauty"
	CategoryHealth               Category = "health"
	CategoryFitness              Category = "fitness"
	CategoryAutomotive           Category = "automotive"
	CategoryHomeServices         Category = "home_services"
	CategoryProfessionalServices Category = "professional_services"
	CategoryHospitality          Category = "hospitality"
	CategoryEducation            Category = "education"
	CategoryEntertainment        Category = "entertainment"

	// CategoryUnknown is a real answer, not an error: some businesses genuinely
	// do not fit, and recording that is what stops us paying to ask again.
	CategoryUnknown Category = "unknown"
)

// orderedCategories is a slice, not a map, because it is rendered into the
// classify prompt — ranging a map would make that prompt non-deterministic,
// the same trap internal/refine's buildPrompt documents.
var orderedCategories = []Category{
	CategoryRestaurant,
	CategoryRetail,
	CategoryBeauty,
	CategoryHealth,
	CategoryFitness,
	CategoryAutomotive,
	CategoryHomeServices,
	CategoryProfessionalServices,
	CategoryHospitality,
	CategoryEducation,
	CategoryEntertainment,
	CategoryUnknown,
}

// Categories returns the closed vocabulary in a fixed order.
func Categories() []Category {
	out := make([]Category, len(orderedCategories))
	copy(out, orderedCategories)
	return out
}

// CategoryStrings is Categories as plain strings, for internal/refine, which
// knows nothing about this vocabulary beyond "these are the allowed answers".
func CategoryStrings() []string {
	out := make([]string, 0, len(orderedCategories))
	for _, c := range orderedCategories {
		out = append(out, string(c))
	}
	return out
}

// Valid reports whether s is a member of the vocabulary.
func Valid(s string) bool {
	_, ok := categorySet[Category(s)]
	return ok
}

var categorySet = func() map[Category]struct{} {
	m := make(map[Category]struct{}, len(orderedCategories))
	for _, c := range orderedCategories {
		m[c] = struct{}{}
	}
	return m
}()

// typeRules maps a Google place type to our category.
//
// Generic types that carry no business meaning — point_of_interest,
// establishment, store, food, premise — are deliberately absent. They are
// exactly the rows the model tier exists for, and mapping them here would turn
// "we do not know" into a confident wrong answer for free.
//
// Editing this table changes cached answers: bump config.LeadgenCategoryVersion
// in the same change.
var typeRules = map[string]Category{
	// Food and drink
	"restaurant":           CategoryRestaurant,
	"cafe":                 CategoryRestaurant,
	"coffee_shop":          CategoryRestaurant,
	"bar":                  CategoryRestaurant,
	"bakery":               CategoryRestaurant,
	"meal_takeaway":        CategoryRestaurant,
	"meal_delivery":        CategoryRestaurant,
	"fast_food_restaurant": CategoryRestaurant,
	"pizza_restaurant":     CategoryRestaurant,
	"ice_cream_shop":       CategoryRestaurant,

	// Retail
	"supermarket":          CategoryRetail,
	"grocery_store":        CategoryRetail,
	"convenience_store":    CategoryRetail,
	"clothing_store":       CategoryRetail,
	"shoe_store":           CategoryRetail,
	"jewelry_store":        CategoryRetail,
	"furniture_store":      CategoryRetail,
	"home_goods_store":     CategoryRetail,
	"electronics_store":    CategoryRetail,
	"hardware_store":       CategoryRetail,
	"book_store":           CategoryRetail,
	"pet_store":            CategoryRetail,
	"florist":              CategoryRetail,
	"liquor_store":         CategoryRetail,
	"department_store":     CategoryRetail,
	"sporting_goods_store": CategoryRetail,

	// Beauty and personal care
	"hair_care":     CategoryBeauty,
	"hair_salon":    CategoryBeauty,
	"barber_shop":   CategoryBeauty,
	"beauty_salon":  CategoryBeauty,
	"nail_salon":    CategoryBeauty,
	"spa":           CategoryBeauty,
	"tattoo_parlor": CategoryBeauty,

	// Health
	"dentist":         CategoryHealth,
	"dental_clinic":   CategoryHealth,
	"doctor":          CategoryHealth,
	"hospital":        CategoryHealth,
	"pharmacy":        CategoryHealth,
	"drugstore":       CategoryHealth,
	"physiotherapist": CategoryHealth,
	"chiropractor":    CategoryHealth,
	"veterinary_care": CategoryHealth,
	"medical_lab":     CategoryHealth,
	"optician":        CategoryHealth,
	"psychologist":    CategoryHealth,

	// Fitness
	"gym":            CategoryFitness,
	"fitness_center": CategoryFitness,
	"yoga_studio":    CategoryFitness,
	"swimming_pool":  CategoryFitness,

	// Automotive
	"car_repair":       CategoryAutomotive,
	"car_dealer":       CategoryAutomotive,
	"car_wash":         CategoryAutomotive,
	"car_rental":       CategoryAutomotive,
	"gas_station":      CategoryAutomotive,
	"auto_parts_store": CategoryAutomotive,
	"tire_shop":        CategoryAutomotive,

	// Home and trade services
	"plumber":                CategoryHomeServices,
	"electrician":            CategoryHomeServices,
	"painter":                CategoryHomeServices,
	"roofing_contractor":     CategoryHomeServices,
	"general_contractor":     CategoryHomeServices,
	"locksmith":              CategoryHomeServices,
	"moving_company":         CategoryHomeServices,
	"storage":                CategoryHomeServices,
	"laundry":                CategoryHomeServices,
	"dry_cleaner":            CategoryHomeServices,
	"house_cleaning_service": CategoryHomeServices,
	"pest_control_service":   CategoryHomeServices,

	// Professional services
	"lawyer":             CategoryProfessionalServices,
	"accounting":         CategoryProfessionalServices,
	"insurance_agency":   CategoryProfessionalServices,
	"real_estate_agency": CategoryProfessionalServices,
	"travel_agency":      CategoryProfessionalServices,
	"bank":               CategoryProfessionalServices,
	"consultant":         CategoryProfessionalServices,
	"advertising_agency": CategoryProfessionalServices,
	"employment_agency":  CategoryProfessionalServices,
	"notary_public":      CategoryProfessionalServices,

	// Hospitality
	"lodging":      CategoryHospitality,
	"hotel":        CategoryHospitality,
	"motel":        CategoryHospitality,
	"hostel":       CategoryHospitality,
	"guest_house":  CategoryHospitality,
	"resort_hotel": CategoryHospitality,
	"campground":   CategoryHospitality,

	// Education
	"school":           CategoryEducation,
	"primary_school":   CategoryEducation,
	"secondary_school": CategoryEducation,
	"university":       CategoryEducation,
	"driving_school":   CategoryEducation,
	"language_school":  CategoryEducation,
	"library":          CategoryEducation,
	"preschool":        CategoryEducation,

	// Entertainment and culture
	"movie_theater":      CategoryEntertainment,
	"night_club":         CategoryEntertainment,
	"amusement_park":     CategoryEntertainment,
	"museum":             CategoryEntertainment,
	"art_gallery":        CategoryEntertainment,
	"tourist_attraction": CategoryEntertainment,
	"casino":             CategoryEntertainment,
	"bowling_alley":      CategoryEntertainment,
	"zoo":                CategoryEntertainment,
}

// CategoryForTypes applies the rule table: primary_type first, then types[] in
// the order Google returned them.
//
// The order matters and is not an implementation detail. Google lists the most
// specific type first, so "the first type we recognise" is the closest match;
// ranging the table instead would make the answer depend on map iteration.
func CategoryForTypes(primaryType string, types []string) (Category, bool) {
	if c, ok := typeRules[primaryType]; ok {
		return c, true
	}
	for _, t := range types {
		if c, ok := typeRules[t]; ok {
			return c, true
		}
	}
	return CategoryUnknown, false
}
