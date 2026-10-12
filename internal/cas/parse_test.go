package cas

import (
	"strings"
	"testing"
)

const authFormFixture = `<!doctype html>
<html><body>
<form id="dynamicFromId">
  <input id="execution" name="execution" value="wrong-form">
  <input id="pwdEncryptSalt" value="short">
</form>
<form id="pwdFromId" method="post" action="/authserver/login">
  <input id="username" name="username" value="">
  <input id="password" name="passwordText" value="">
  <input id="saltPassword" name="password" type="hidden">
  <input id="_eventId" name="_eventId" value="submit">
  <input id="cllt" name="cllt" value="userNameLogin">
  <input id="dllt" name="dllt" value="generalLogin">
  <input id="lt" name="lt" value="">
  <input id="pwdEncryptSalt" value="feINsdNKS0tk5gxb">
  <input id="execution" name="execution" value="e1s1">
  <div id="formErrorTip"><span id="showErrorTip"> </span></div>
</form>
<p id="current-login-type">adapter</p>
<p id="login-error-code"></p>
</body></html>`

func TestParseLoginFormRequiresFieldsInPwdFromID(t *testing.T) {
	form, err := parseLoginForm(strings.NewReader(authFormFixture))
	if err != nil {
		t.Fatalf("parseLoginForm: %v", err)
	}
	if form.Execution != "e1s1" || form.LT != "" || form.EventID != "submit" || form.CLLT != "userNameLogin" || form.DLLT != "generalLogin" {
		t.Fatalf("form fields = %+v", form)
	}
	if form.Salt != "feINsdNKS0tk5gxb" || len(form.Salt) != 16 {
		t.Fatalf("salt = %q", form.Salt)
	}
	if form.Action != "/authserver/login" {
		t.Fatalf("action = %q", form.Action)
	}
}

func TestParseLoginFormRejectsMissingRequiredField(t *testing.T) {
	bad := strings.Replace(authFormFixture, `name="execution" value="e1s1"`, `name="not-execution" value="e1s1"`, 1)
	if _, err := parseLoginForm(strings.NewReader(bad)); err == nil {
		t.Fatal("parseLoginForm accepted missing execution")
	}
}

func TestParseLoginFormRejectsMissingLT(t *testing.T) {
	bad := strings.Replace(authFormFixture, `  <input id="lt" name="lt" value="">`+"\n", "", 1)
	if bad == authFormFixture {
		t.Fatal("fixture no longer contains the lt input")
	}
	if _, err := parseLoginForm(strings.NewReader(bad)); err == nil {
		t.Fatal("parseLoginForm accepted a form without lt")
	}
}

func TestParseAdapterLoginType(t *testing.T) {
	got, err := parseElementText(strings.NewReader(authFormFixture), "current-login-type")
	if err != nil || got != "adapter" {
		t.Fatalf("login type = %q, err = %v", got, err)
	}
}

func TestParseErrorTipIsScopedToPasswordForm(t *testing.T) {
	html := strings.Replace(authFormFixture, `<span id="showErrorTip"> </span>`, `<span id="showErrorTip">用户名或密码有误</span>`, 1)
	got, err := parseErrorTip(strings.NewReader(html))
	if err != nil || got != "用户名或密码有误" {
		t.Fatalf("error tip = %q, err = %v", got, err)
	}
}
