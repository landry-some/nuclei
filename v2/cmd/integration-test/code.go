package main

import (
	"errors"
	"log"
	"os"
	"path/filepath"

	osutils "github.com/projectdiscovery/utils/os"

	"github.com/projectdiscovery/nuclei/v2/pkg/templates/signer"
	"github.com/projectdiscovery/nuclei/v2/pkg/testutils"
	"github.com/projectdiscovery/nuclei/v2/pkg/utils"
)

var codeTestCases = []TestCaseInfo{
	{Path: "protocols/code/py-snippet.yaml", TestCase: &codeSnippet{}},
	{Path: "protocols/code/py-file.yaml", TestCase: &codeFile{}},
	{Path: "protocols/code/py-env-var.yaml", TestCase: &codeEnvVar{}},
	{Path: "protocols/code/unsigned.yaml", TestCase: &unsignedCode{}},
	{Path: "protocols/code/rsa-signed.yaml", TestCase: &rsaSignedCode{}},
	{Path: "protocols/code/py-interactsh.yaml", TestCase: &codeSnippet{}},
	{Path: "protocols/code/ps1-snippet.yaml", TestCase: &codeSnippet{}, DisableOn: func() bool { return !osutils.IsWindows() }},
}

var (
	ecdsaPrivateKeyAbsPath string
	ecdsaPublicKeyAbsPath  string

	// rsaPrivateKeyAbsPath string
	rsaPublicKeyAbsPath string
)

func init() {
	var err error
	ecdsaPrivateKeyAbsPath, err = filepath.Abs("protocols/code/ecdsa-priv-key.pem")
	if err != nil {
		panic(err)
	}
	ecdsaPublicKeyAbsPath, err = filepath.Abs("protocols/code/ecdsa-pub-key.pem")
	if err != nil {
		panic(err)
	}

	// rsaPrivateKeyAbsPath, err = filepath.Abs("protocols/code/rsa-priv-key.pem")
	// if err != nil {
	// 	panic(err)
	// }
	rsaPublicKeyAbsPath, err = filepath.Abs("protocols/code/rsa-pub-key.pem")
	if err != nil {
		panic(err)
	}

	signTemplates()
}

// signTemplates tests the signing procedure on various platforms
func signTemplates() {
	signerOptions := &signer.Options{
		PrivateKeyName: ecdsaPrivateKeyAbsPath,
		PublicKeyName:  ecdsaPublicKeyAbsPath,
		Algorithm:      signer.ECDSA,
	}
	sign, err := signer.New(signerOptions)
	if err != nil {
		log.Fatalf("couldn't create crypto engine: %s\n", err)
	}

	for _, v := range codeTestCases {
		templatePath := v.Path
		testCase := v.TestCase

		if v.DisableOn != nil && v.DisableOn() {
			// skip ps1 test case on non-windows platforms
			continue
		}

		templatePath, err := filepath.Abs(templatePath)
		if err != nil {
			panic(err)
		}

		// skip
		// - unsigned test case
		if _, ok := testCase.(*unsignedCode); ok {
			continue
		}
		// - already rsa signed
		if _, ok := testCase.(*rsaSignedCode); ok {
			continue
		}

		if err := utils.ProcessFile(sign, templatePath); err != nil {
			log.Fatalf("Could not walk directory: %s\n", err)
		}
	}
}

func prepareEnv(keypath string) {
	os.Setenv("NUCLEI_SIGNATURE_PUBLIC_KEY", keypath)
	os.Setenv("NUCLEI_SIGNATURE_ALGORITHM", "ecdsa")
}

func tearDownEnv() {
	os.Unsetenv("NUCLEI_SIGNATURE_PUBLIC_KEY")
	os.Unsetenv("NUCLEI_SIGNATURE_ALGORITHM")
}

type codeSnippet struct{}

// Execute executes a test case and returns an error if occurred
func (h *codeSnippet) Execute(filePath string) error {
	prepareEnv(ecdsaPublicKeyAbsPath)
	defer tearDownEnv()

	results, err := testutils.RunNucleiTemplateAndGetResults(filePath, "input", debug)
	if err != nil {
		return err
	}
	return expectResultsCount(results, 1)
}

type codeFile struct{}

// Execute executes a test case and returns an error if occurred
func (h *codeFile) Execute(filePath string) error {
	prepareEnv(ecdsaPublicKeyAbsPath)
	defer tearDownEnv()

	results, err := testutils.RunNucleiTemplateAndGetResults(filePath, "input", debug)
	if err != nil {
		return err
	}
	return expectResultsCount(results, 1)
}

type codeEnvVar struct{}

// Execute executes a test case and returns an error if occurred
func (h *codeEnvVar) Execute(filePath string) error {
	prepareEnv(ecdsaPublicKeyAbsPath)
	defer tearDownEnv()

	results, err := testutils.RunNucleiTemplateAndGetResults(filePath, "input", debug, "-V", "baz=baz")
	if err != nil {
		return err
	}
	return expectResultsCount(results, 1)
}

type unsignedCode struct{}

// Execute executes a test case and returns an error if occurred
func (h *unsignedCode) Execute(filePath string) error {
	prepareEnv(ecdsaPublicKeyAbsPath)
	defer tearDownEnv()

	results, err := testutils.RunNucleiTemplateAndGetResults(filePath, "input", debug)

	// should error out
	if err != nil {
		return nil
	}

	// this point should never be reached
	return errors.Join(expectResultsCount(results, 1), errors.New("unsigned template was executed"))
}

type rsaSignedCode struct{}

// Execute executes a test case and returns an error if occurred
func (h *rsaSignedCode) Execute(filePath string) error {
	prepareEnv(rsaPublicKeyAbsPath)
	defer tearDownEnv()

	results, err := testutils.RunNucleiTemplateAndGetResults(filePath, "input", debug)

	// should error out
	if err != nil {
		return nil
	}

	// this point should never be reached
	return errors.Join(expectResultsCount(results, 1), errors.New("unsigned template was executed"))
}
