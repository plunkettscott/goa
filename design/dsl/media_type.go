package dsl

import (
	"fmt"
	"mime"
	"strings"

	"bitbucket.org/pkg/inflect"
	"github.com/raphael/goa/design"
)

// Counter used to create unique media type names for identifier-less media types.
var mediaTypeCount int

// MediaType implements the media type definition DSL. A media type definition describes the
// representation of a resource used in a response body. This includes listing all the *potential*
// resource attributes that can appear in the body. Views specify which of the attributes are
// actually rendered so that the same media type definition may represent multiple rendering of a
// given resource representation.
//
// All media types must define a view named "default". This view is used to render the media type in
// response bodies when no other view is specified.
//
// A media type definition may also define links to other media types. This is done by first
// defining an attribute for the linked-to media type and then referring to that attribute in the
// Links DSL. Views may then elect to render one or the other or both. Links are rendered using the
// special "link" view. Media types that are linked to must define that view. Here is an example
// showing all the possible media type sub-definitions:
//
// 	MediaType("application/vnd.goa.example.bottle", func() {
//		Description("A bottle of wine")
//		Attributes(func() {
//			Attribute("id", Integer, "ID of bottle")
//			Attribute("href", String, "API href of bottle")
//			Attribute("account", Account, "Owner account")
//			Attribute("origin", Origin, "Details on wine origin")
//			Links(func() {
//				Link("account")        // Defines a link to the Account media type
//				Link("origin", "tiny") // Overrides the default view used to render links
//			})
//              	Required("id", "href")
//     		 })
//		View("default", func() {
//			Attribute("id")
//			Attribute("href")
//			Attribute("links") // Default view renders links
//		})
//		View("extended", func() {
//			Attribute("id")
//			Attribute("href")
//			Attribute("account") // Extended view renders account inline
//			Attribute("origin")  // Extended view renders origin inline
//			Attribute("links")   // Extended view also renders links
//		})
// 	})
//
// This function returns the media type definition so it can be referred to throughout the DSL.
func MediaType(identifier string, dsl func()) *design.MediaTypeDefinition {
	if design.Design == nil {
		InitDesign()
	}
	if design.Design.MediaTypes == nil {
		design.Design.MediaTypes = make(map[string]*design.MediaTypeDefinition)
	}
	if topLevelDefinition(true) {
		identifier, params, err := mime.ParseMediaType(identifier)
		if err != nil {
			ReportError("invalid media type identifier %#v: %s",
				identifier, err)
		}
		slash := strings.Index(identifier, "/")
		if slash == -1 {
			identifier += "/json"
		}
		identifier = mime.FormatMediaType(identifier, params)
		elems := strings.Split(identifier, ".")
		elems = strings.Split(elems[len(elems)-1], "/")
		typeName := inflect.Camelize(elems[0])
		if typeName == "" {
			mediaTypeCount++
			typeName = fmt.Sprintf("MediaType%d", mediaTypeCount)
		}
		if _, ok := design.Design.MediaTypes[identifier]; ok {
			ReportError("media type %#v is defined twice", identifier)
			return nil
		}
		mt := design.NewMediaTypeDefinition(typeName, identifier, dsl)
		design.Design.MediaTypes[identifier] = mt
		return mt
	}
	return nil
}

// Media sets a response media type by name or by reference using a value returned by MediaType:
//
// 	Response("NotFound", func() {
//		Status(404)
//		Media("application/json")
//	})
//
// Media can be used inside Response or ResponseTemplate.
func Media(val interface{}) {
	if r, ok := responseDefinition(true); ok {
		if m, ok := val.(*design.MediaTypeDefinition); ok {
			r.MediaType = m.Identifier
		} else if identifier, ok := val.(string); ok {
			r.MediaType = identifier
		} else {
			ReportError("media type must be a string or a pointer to MediaTypeDefinition, got %#v", val)
		}
	}
}

// Reference sets a type or media type reference. The value itself can be a type or a media type.
// The reference type attributes define the default properties for attributes with the same name in
// the type using the reference. So for example if a type is defined as such:
//
// 	var Bottle = Type("bottle", func() {
//		Attribute("name", func() {
//			MinLength(3)
//		})
//		Attribute("vintage", Integer, func() {
//			Minimum(1970)
//		})
//		Attribute("somethingelse")
//	})
//
// Declaring the following media type:
//
// 	var BottleMedia = MediaType("vnd.goa.bottle", func() {
//		Reference(Bottle)
//		Attributes(func() {
//			Attribute("id", Integer)
//			Attribute("name")
//			Attribute("vintage")
//		})
//	})
//
// defines the "name" and "vintage" attributes with the same type and validations as defined in
// the Bottle type.
func Reference(t design.DataType) {
	if mt, ok := mediaTypeDefinition(false); ok {
		mt.Reference = t
	} else if ut, ok := typeDefinition(true); ok {
		ut.Reference = t
	}
}

// TypeName makes it possible to set the Go struct name for a type or media type in the generated
// code. By default goagen uses the name (type) or identifier (media type) given in the DSL and
// computes a valid Go identifier from it. This function makes it possible to override that and
// provide a custom name. name must be a valid Go identifier.
func TypeName(name string) {
	if mt, ok := mediaTypeDefinition(false); ok {
		mt.TypeName = name
	} else if ut, ok := typeDefinition(true); ok {
		ut.TypeName = name
	}
}

// View adds a new view to a media type. A view has a name and lists attributes that are
// rendered when the view is used to produce a response. The attribute names must appear in the
// media type definition. If an attribute is itself a media type then the view may specify which
// view to use when rendering the attribute using the View function in the View DSL. If not
// specified then the view named "default" is used. Examples:
//
//	View("default", func() {
//		Attribute("id")         // "id" and "name" must be media type attributes
//		Attribute("name")
//	})
//
//	View("extended", func() {
//		Attribute("id")
//		Attribute("name")
//		Attribute("origin", func() {
//			View("extended") // Use view "extended" to render attribute "origin"
//		})
//	})
func View(name string, dsl ...func()) {
	if mt, ok := mediaTypeDefinition(false); ok {
		if !mt.Type.IsObject() && !mt.Type.IsArray() {
			ReportError("cannot define view on non object and non collection media types")
			return
		}
		if mt.Views == nil {
			mt.Views = make(map[string]*design.ViewDefinition)
		} else {
			if _, ok = mt.Views[name]; ok {
				ReportError("multiple definitions for view %#v in media type %#v", name, mt.TypeName)
				return
			}
		}
		at := &design.AttributeDefinition{}
		ok := false
		if len(dsl) > 0 {
			ok = executeDSL(dsl[0], at)
		} else if mt.Type.IsArray() {
			// inherit view from collection element if present
			elem := mt.Type.ToArray().ElemType
			if elem != nil {
				if pa, ok2 := elem.Type.(*design.MediaTypeDefinition); ok2 {
					if v, ok2 := pa.Views[name]; ok2 {
						at = v.AttributeDefinition
						ok = true
					} else {
						ReportError("unknown view %#v", name)
						return
					}
				}
			}
		}
		if ok {
			o := at.Type.ToObject()
			if o != nil {
				mto := mt.Type.ToObject()
				if mto == nil {
					mto = mt.Type.ToArray().ElemType.Type.ToObject()
				}
				for n := range o {
					if existing, ok := mto[n]; ok {
						o[n] = existing
					} else if n != "links" {
						ReportError("unknown attribute %#v", n)
					}
				}
			}
			mt.Views[name] = &design.ViewDefinition{
				AttributeDefinition: at,
				Name:                name,
				Parent:              mt,
			}
		}
	} else if a, ok := attributeDefinition(true); ok {
		a.View = name
	}
}

// Attributes implements the media type attributes DSL. See MediaType.
func Attributes(dsl func()) {
	if mt, ok := mediaTypeDefinition(true); ok {
		executeDSL(dsl, mt)
	}
}

// Links implements the media type links DSL. See MediaType.
func Links(dsl func()) {
	if mt, ok := mediaTypeDefinition(true); ok {
		executeDSL(dsl, mt)
	}
}

// Link adds a link to a media type. At the minimum a link has a name corresponding to one of the
// media type attribute names. A link may also define the view used to render the linked-to
// attribute. The default view used to render links is "link". Examples:
//
// 	Link("origin")           // Use the "link" view of the "origin" attribute
//	Link("account", "tiny")  // Use the "tiny" view of the "account" attribute
func Link(name string, view ...string) {
	if mt, ok := mediaTypeDefinition(true); ok {
		if mt.Links == nil {
			mt.Links = make(map[string]*design.LinkDefinition)
		} else {
			if _, ok := mt.Links[name]; ok {
				ReportError("duplicate definition for link %#v", name)
				return
			}
		}
		link := &design.LinkDefinition{Name: name, Parent: mt}
		if len(view) > 1 {
			ReportError("invalid syntax in Link definition for %#v, allowed syntax is Link(name) or Link(name, view)", name)
		}
		if len(view) > 0 {
			link.View = view[0]
		} else {
			link.View = "link"
		}
		mt.Links[name] = link
	}
}

// CollectionOf creates a collection media type from its element media type. A collection media
// type represents the content of responses that return a collection of resources such as "list"
// actions. This function can be called from any place where a media type can be used.
// The resulting media type identifier is built from the element media type by appending the media
// type parameter "type" with value "collection".
func CollectionOf(m *design.MediaTypeDefinition, dsl ...func()) *design.MediaTypeDefinition {
	id := m.Identifier
	mediatype, params, err := mime.ParseMediaType(id)
	if err != nil {
		ReportError("invalid media type identifier %#v: %s", id, err)
		return nil
	}
	slash := strings.Index(mediatype, "/")
	if slash == -1 {
		mediatype += "/json"
	}
	hasType := false
	for param := range params {
		if param == "type" {
			hasType = true
			break
		}
	}
	if !hasType {
		params["type"] = "collection"
	}
	id = mime.FormatMediaType(mediatype, params)
	typeName := m.TypeName + "Collection"
	mt := design.NewMediaTypeDefinition(typeName, id, func() {
		if mt, ok := mediaTypeDefinition(true); ok {
			mt.TypeName = typeName
			mt.AttributeDefinition = &design.AttributeDefinition{Type: ArrayOf(m)}
			if len(dsl) > 0 {
				executeDSL(dsl[0], mt)
			}
		}
	})
	if executeDSL(mt.DSL, mt) {
		design.Design.MediaTypes[id] = mt
	}
	return mt
}