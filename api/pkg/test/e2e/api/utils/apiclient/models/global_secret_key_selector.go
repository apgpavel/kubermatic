// Code generated by go-swagger; DO NOT EDIT.

package models

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	strfmt "github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
)

// GlobalSecretKeySelector global secret key selector
// swagger:model GlobalSecretKeySelector
type GlobalSecretKeySelector struct {
	GlobalObjectKeySelector
}

// UnmarshalJSON unmarshals this object from a JSON structure
func (m *GlobalSecretKeySelector) UnmarshalJSON(raw []byte) error {
	// AO0
	var aO0 GlobalObjectKeySelector
	if err := swag.ReadJSON(raw, &aO0); err != nil {
		return err
	}
	m.GlobalObjectKeySelector = aO0

	return nil
}

// MarshalJSON marshals this object to a JSON structure
func (m GlobalSecretKeySelector) MarshalJSON() ([]byte, error) {
	_parts := make([][]byte, 0, 1)

	aO0, err := swag.WriteJSON(m.GlobalObjectKeySelector)
	if err != nil {
		return nil, err
	}
	_parts = append(_parts, aO0)

	return swag.ConcatJSON(_parts...), nil
}

// Validate validates this global secret key selector
func (m *GlobalSecretKeySelector) Validate(formats strfmt.Registry) error {
	return nil
}

// MarshalBinary interface implementation
func (m *GlobalSecretKeySelector) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return swag.WriteJSON(m)
}

// UnmarshalBinary interface implementation
func (m *GlobalSecretKeySelector) UnmarshalBinary(b []byte) error {
	var res GlobalSecretKeySelector
	if err := swag.ReadJSON(b, &res); err != nil {
		return err
	}
	*m = res
	return nil
}