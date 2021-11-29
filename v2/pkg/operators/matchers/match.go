package matchers

import (
	"strings"

	"github.com/projectdiscovery/nuclei/v2/pkg/protocols/common/expressions"
)

// MatchStatusCode matches a status code check against a corpus
func (matcher *Matcher) MatchStatusCode(statusCode int) bool {
	// Iterate over all the status codes accepted as valid
	//
	// Status codes don't support AND conditions.
	for _, status := range matcher.Status {
		// Continue if the status codes don't match
		if statusCode != status {
			continue
		}
		// Return on the first match.
		return true
	}
	return false
}

// MatchSize matches a size check against a corpus
func (matcher *Matcher) MatchSize(length int) bool {
	// Iterate over all the sizes accepted as valid
	//
	// Sizes codes don't support AND conditions.
	for _, size := range matcher.Size {
		// Continue if the size doesn't match
		if length != size {
			continue
		}
		// Return on the first match.
		return true
	}
	return false
}

// MatchWords matches a word check against a corpus.
func (matcher *Matcher) MatchWords(corpus string, dynamicValues map[string]interface{}) (bool, []string) {
	if matcher.CaseInsensitive {
		corpus = strings.ToLower(corpus)
	}

	var matchedWords []string
	// Iterate over all the words accepted as valid
	for i, word := range matcher.Words {
		if dynamicValues == nil {
			dynamicValues = make(map[string]interface{})
		}

		var err error
		word, err = expressions.Evaluate(word, dynamicValues)
		if err != nil {
			continue
		}
		// Continue if the word doesn't match
		if !strings.Contains(corpus, word) {
			// If we are in an AND request and a match failed,
			// return false as the AND condition fails on any single mismatch.
			if matcher.condition == ANDCondition {
				return false, []string{}
			}
			// Continue with the flow since it's an OR Condition.
			continue
		}

		// If the condition was an OR, return on the first match.
		if matcher.condition == ORCondition {
			return true, []string{word}
		}

		matchedWords = append(matchedWords, word)

		// If we are at the end of the words, return with true
		if len(matcher.Words)-1 == i {
			return true, matchedWords
		}
	}
	return false, []string{}
}

// MatchRegex matches a regex check against a corpus
func (matcher *Matcher) MatchRegex(corpus string) (bool, []string) {
	var matchedRegexes []string
	// Iterate over all the regexes accepted as valid
	for i, regex := range matcher.regexCompiled {
		// Continue if the regex doesn't match
		if !regex.MatchString(corpus) {
			// If we are in an AND request and a match failed,
			// return false as the AND condition fails on any single mismatch.
			if matcher.condition == ANDCondition {
				return false, []string{}
			}
			// Continue with the flow since it's an OR Condition.
			continue
		}

		currentMatches := regex.FindAllString(corpus, -1)
		// If the condition was an OR, return on the first match.
		if matcher.condition == ORCondition {
			return true, currentMatches
		}

		matchedRegexes = append(matchedRegexes, currentMatches...)

		// If we are at the end of the regex, return with true
		if len(matcher.regexCompiled)-1 == i {
			return true, matchedRegexes
		}
	}
	return false, []string{}
}

// MatchBinary matches a binary check against a corpus
func (matcher *Matcher) MatchBinary(corpus string) (bool, []string) {
	var matchedBinary []string
	// Iterate over all the words accepted as valid
	for i, binary := range matcher.binaryDecoded {
		if !strings.Contains(corpus, binary) {
			// If we are in an AND request and a match failed,
			// return false as the AND condition fails on any single mismatch.
			if matcher.condition == ANDCondition {
				return false, []string{}
			}
			// Continue with the flow since it's an OR Condition.
			continue
		}

		// If the condition was an OR, return on the first match.
		if matcher.condition == ORCondition {
			return true, []string{binary}
		}

		matchedBinary = append(matchedBinary, binary)

		// If we are at the end of the words, return with true
		if len(matcher.Binary)-1 == i {
			return true, matchedBinary
		}
	}
	return false, []string{}
}

// MatchDSL matches on a generic map result
func (matcher *Matcher) MatchDSL(data map[string]interface{}) bool {
	// Iterate over all the expressions accepted as valid
	for i, expression := range matcher.dslCompiled {
		result, err := expression.Evaluate(data)
		if err != nil {
			continue
		}

		var bResult bool
		bResult, ok := result.(bool)

		// Continue if the regex doesn't match
		if !ok || !bResult {
			// If we are in an AND request and a match failed,
			// return false as the AND condition fails on any single mismatch.
			if matcher.condition == ANDCondition {
				return false
			}
			// Continue with the flow since it's an OR Condition.
			continue
		}

		// If the condition was an OR, return on the first match.
		if matcher.condition == ORCondition {
			return true
		}

		// If we are at the end of the dsl, return with true
		if len(matcher.dslCompiled)-1 == i {
			return true
		}
	}
	return false
}
