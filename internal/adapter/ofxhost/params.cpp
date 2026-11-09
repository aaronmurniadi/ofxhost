/*
 * OfxHost - an OpenFX host for still images.
 *
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

/* The parameter classes, moved out of host.cpp.
 *
 * They were 269 lines in the middle of the host: every OFX parameter type needs a concrete
 * host class because the library leaves the get and set of each shape to the host, and each
 * class holds one value and reports it. None of them touches the instance, the clips or the
 * render, so they were the one part of that file with a seam already in it.
 *
 * What stayed in host.cpp is the call that reaches them. A type this file does not implement
 * returns nullptr and the caller reports it, because reporting goes through the instance's
 * callbacks and those belong to the instance, not to a parameter.
 */

#include "ofxhost_params.h"

#include "ofxCore.h"
#include "ofxParam.h"
#include "ofxhParam.h"

#include <string>

namespace ofxhost {

/* Reports whether an optional flag property is set, so a host answers "no" rather than
 * inventing a value. Declared in ofxhost_params.h because host.cpp reads the same flags when
 * it builds a parameter description. */
bool flagProperty(const OFX::Host::Param::Instance &param, const char *name) {
  try {
    return param.getProperties().getIntProperty(name, 0) != 0;
  } catch (...) {
    return false;
  }
}

}  // namespace ofxhost

namespace {

using ofxhost::flagProperty;

/* Every OFX parameter type needs a concrete host class, because the library
 * leaves the get and set of each shape to the host. They all do the same thing:
 * hold a value of the right shape and report it. A still-image host has one time,
 * so the time-based overloads ignore their argument; a keyframe-aware host would
 * not, and nothing here pretends to be one.
 *
 * The values are held as doubles even for the integer types, because that is the
 * shape the parameter suite and the pipeline both use: the host reads a number and
 * the plugin gets an int back.
 */

class IntegerImpl : public OFX::Host::Param::IntegerInstance {
 public:
  IntegerImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::IntegerInstance(desc, set) {
    value_ = static_cast<int>(desc.getProperties().getDoubleProperty(kOfxParamPropDefault, 0));
  }
  OfxStatus get(int &v) override { v = value_; return kOfxStatOK; }
  OfxStatus get(OfxTime, int &v) override { v = value_; return kOfxStatOK; }
  OfxStatus set(int v) override { value_ = v; return kOfxStatOK; }
  OfxStatus set(OfxTime, int v) override { value_ = v; return kOfxStatOK; }

 private:
  int value_ = 0;
};

class DoubleImpl : public OFX::Host::Param::DoubleInstance {
 public:
  DoubleImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::DoubleInstance(desc, set) {
    value_ = desc.getProperties().getDoubleProperty(kOfxParamPropDefault, 0);
  }
  OfxStatus get(double &v) override { v = value_; return kOfxStatOK; }
  OfxStatus get(OfxTime, double &v) override { v = value_; return kOfxStatOK; }
  OfxStatus set(double v) override { value_ = v; return kOfxStatOK; }
  OfxStatus set(OfxTime, double v) override { value_ = v; return kOfxStatOK; }
  // A still image has no animation, so a derivative is zero and an integral over
  // one point is the value itself.
  OfxStatus derive(OfxTime, double &v) override { v = 0.0; return kOfxStatOK; }
  OfxStatus integrate(OfxTime, OfxTime, double &v) override { v = value_; return kOfxStatOK; }

 private:
  double value_ = 0.0;
};

class BooleanImpl : public OFX::Host::Param::BooleanInstance {
 public:
  BooleanImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::BooleanInstance(desc, set) {
    value_ = desc.getProperties().getIntProperty(kOfxParamPropDefault, 0) != 0;
  }
  OfxStatus get(bool &v) override { v = value_; return kOfxStatOK; }
  OfxStatus get(OfxTime, bool &v) override { v = value_; return kOfxStatOK; }
  OfxStatus set(bool v) override { value_ = v; return kOfxStatOK; }
  OfxStatus set(OfxTime, bool v) override { value_ = v; return kOfxStatOK; }

 private:
  bool value_ = false;
};

class ChoiceImpl : public OFX::Host::Param::ChoiceInstance {
 public:
  ChoiceImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::ChoiceInstance(desc, set) {
    value_ = static_cast<int>(desc.getProperties().getDoubleProperty(kOfxParamPropDefault, 0));
  }
  OfxStatus get(int &v) override { v = value_; return kOfxStatOK; }
  OfxStatus get(OfxTime, int &v) override { v = value_; return kOfxStatOK; }
  OfxStatus set(int v) override { value_ = v; return kOfxStatOK; }
  OfxStatus set(OfxTime, int v) override { value_ = v; return kOfxStatOK; }

 private:
  int value_ = 0;
};

class StringImpl : public OFX::Host::Param::StringInstance {
 public:
  StringImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::StringInstance(desc, set) {
    try {
      value_ = desc.getProperties().getStringProperty(kOfxParamPropDefault, 0);
    } catch (...) {
      value_.clear();
    }
  }
  OfxStatus get(std::string &v) override { v = value_; return kOfxStatOK; }
  OfxStatus get(OfxTime, std::string &v) override { v = value_; return kOfxStatOK; }
  OfxStatus set(const char *v) override { value_ = v != nullptr ? v : ""; return kOfxStatOK; }
  OfxStatus set(OfxTime, const char *v) override { value_ = v != nullptr ? v : ""; return kOfxStatOK; }

 private:
  std::string value_;
};

class PushButtonImpl : public OFX::Host::Param::PushbuttonInstance {
 public:
  PushButtonImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::PushbuttonInstance(desc, set) {}
};

/* A custom parameter carries an opaque value the host stores and hands back. The
 * pipeline never reads it; it exists because a plugin that declares one would fail
 * to instantiate without it. */
class CustomImpl : public OFX::Host::Param::CustomInstance {
 public:
  CustomImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set)
      : OFX::Host::Param::CustomInstance(desc, set) {}
  OfxStatus get(std::string &v) override { v = value_; return kOfxStatOK; }
  OfxStatus get(OfxTime, std::string &v) override { v = value_; return kOfxStatOK; }
  OfxStatus set(const char *v) override { value_ = v != nullptr ? v : ""; return kOfxStatOK; }
  OfxStatus set(OfxTime, const char *v) override { value_ = v != nullptr ? v : ""; return kOfxStatOK; }

 private:
  std::string value_;
};

class GroupImpl : public OFX::Host::Param::GroupInstance {
 public:
  explicit GroupImpl(OFX::Host::Param::Descriptor &desc) : OFX::Host::Param::GroupInstance(desc, nullptr) {}
};

class PageImpl : public OFX::Host::Param::PageInstance {
 public:
  explicit PageImpl(OFX::Host::Param::Descriptor &desc) : OFX::Host::Param::PageInstance(desc, nullptr) {}
};

/* The multi-component types each have their own virtual signatures, so each gets
 * its own small class. They share one value holder to keep the arithmetic in one
 * place: four doubles, of which the type uses two, three or four. */
struct Vec4 {
  double v[4] = {0, 0, 0, 0};

  static Vec4 fromDefaults(const OFX::Host::Param::Descriptor &desc, int dimension) {
    Vec4 out;
    for (int i = 0; i < dimension && i < 4; ++i) {
      try {
        out.v[i] = desc.getProperties().getDoubleProperty(kOfxParamPropDefault, i);
      } catch (...) {
        out.v[i] = 0.0;
      }
    }
    return out;
  }
};

class Double2DImpl : public OFX::Host::Param::Double2DInstance {
 public:
  Double2DImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set, int dimension)
      : OFX::Host::Param::Double2DInstance(desc, set), values_(Vec4::fromDefaults(desc, dimension)) {}
  OfxStatus get(double &a, double &b) override { a = values_.v[0]; b = values_.v[1]; return kOfxStatOK; }
  OfxStatus get(OfxTime, double &a, double &b) override { return get(a, b); }
  OfxStatus set(double a, double b) override { values_.v[0] = a; values_.v[1] = b; return kOfxStatOK; }
  OfxStatus set(OfxTime, double a, double b) override { return set(a, b); }

 private:
  Vec4 values_;
};

class Integer2DImpl : public OFX::Host::Param::Integer2DInstance {
 public:
  Integer2DImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set, int dimension)
      : OFX::Host::Param::Integer2DInstance(desc, set), values_(Vec4::fromDefaults(desc, dimension)) {}
  OfxStatus get(int &a, int &b) override {
    a = static_cast<int>(values_.v[0]);
    b = static_cast<int>(values_.v[1]);
    return kOfxStatOK;
  }
  OfxStatus get(OfxTime, int &a, int &b) override { return get(a, b); }
  OfxStatus set(int a, int b) override { values_.v[0] = a; values_.v[1] = b; return kOfxStatOK; }
  OfxStatus set(OfxTime, int a, int b) override { return set(a, b); }

 private:
  Vec4 values_;
};

class Double3DImpl : public OFX::Host::Param::Double3DInstance {
 public:
  Double3DImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set, int dimension)
      : OFX::Host::Param::Double3DInstance(desc, set), values_(Vec4::fromDefaults(desc, dimension)) {}
  OfxStatus get(double &a, double &b, double &c) override {
    a = values_.v[0]; b = values_.v[1]; c = values_.v[2];
    return kOfxStatOK;
  }
  OfxStatus get(OfxTime, double &a, double &b, double &c) override { return get(a, b, c); }
  OfxStatus set(double a, double b, double c) override {
    values_.v[0] = a; values_.v[1] = b; values_.v[2] = c;
    return kOfxStatOK;
  }
  OfxStatus set(OfxTime, double a, double b, double c) override { return set(a, b, c); }

 private:
  Vec4 values_;
};

class Integer3DImpl : public OFX::Host::Param::Integer3DInstance {
 public:
  Integer3DImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set, int dimension)
      : OFX::Host::Param::Integer3DInstance(desc, set), values_(Vec4::fromDefaults(desc, dimension)) {}
  OfxStatus get(int &a, int &b, int &c) override {
    a = static_cast<int>(values_.v[0]);
    b = static_cast<int>(values_.v[1]);
    c = static_cast<int>(values_.v[2]);
    return kOfxStatOK;
  }
  OfxStatus get(OfxTime, int &a, int &b, int &c) override { return get(a, b, c); }
  OfxStatus set(int a, int b, int c) override {
    values_.v[0] = a; values_.v[1] = b; values_.v[2] = c;
    return kOfxStatOK;
  }
  OfxStatus set(OfxTime, int a, int b, int c) override { return set(a, b, c); }

 private:
  Vec4 values_;
};

class RGBImpl : public OFX::Host::Param::RGBInstance {
 public:
  RGBImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set, int dimension)
      : OFX::Host::Param::RGBInstance(desc, set), values_(Vec4::fromDefaults(desc, dimension)) {}
  OfxStatus get(double &a, double &b, double &c) override {
    a = values_.v[0]; b = values_.v[1]; c = values_.v[2];
    return kOfxStatOK;
  }
  OfxStatus get(OfxTime, double &a, double &b, double &c) override { return get(a, b, c); }
  OfxStatus set(double a, double b, double c) override {
    values_.v[0] = a; values_.v[1] = b; values_.v[2] = c;
    return kOfxStatOK;
  }
  OfxStatus set(OfxTime, double a, double b, double c) override { return set(a, b, c); }

 private:
  Vec4 values_;
};

class RGBAImpl : public OFX::Host::Param::RGBAInstance {
 public:
  RGBAImpl(OFX::Host::Param::Descriptor &desc, OFX::Host::Param::SetInstance *set, int dimension)
      : OFX::Host::Param::RGBAInstance(desc, set), values_(Vec4::fromDefaults(desc, dimension)) {}
  OfxStatus get(double &a, double &b, double &c, double &d) override {
    a = values_.v[0]; b = values_.v[1]; c = values_.v[2]; d = values_.v[3];
    return kOfxStatOK;
  }
  OfxStatus get(OfxTime, double &a, double &b, double &c, double &d) override { return get(a, b, c, d); }
  OfxStatus set(double a, double b, double c, double d) override {
    values_.v[0] = a; values_.v[1] = b; values_.v[2] = c; values_.v[3] = d;
    return kOfxStatOK;
  }
  OfxStatus set(OfxTime, double a, double b, double c, double d) override { return set(a, b, c, d); }

 private:
  Vec4 values_;
};

}  // namespace

namespace ofxhost {

OFX::Host::Param::Instance *createParam(OFX::Host::Param::Descriptor &desc,
                                        OFX::Host::Param::SetInstance *set) {
  const std::string type = desc.getType();

  if (type == kOfxParamTypeInteger) {
    return new IntegerImpl(desc, set);
  }
  if (type == kOfxParamTypeDouble) {
    return new DoubleImpl(desc, set);
  }
  if (type == kOfxParamTypeBoolean) {
    return new BooleanImpl(desc, set);
  }
  if (type == kOfxParamTypeChoice) {
    return new ChoiceImpl(desc, set);
  }
  if (type == kOfxParamTypeString) {
    return new StringImpl(desc, set);
  }
  if (type == kOfxParamTypePushButton) {
    return new PushButtonImpl(desc, set);
  }
  if (type == kOfxParamTypeGroup) {
    return new GroupImpl(desc);
  }
  if (type == kOfxParamTypePage) {
    return new PageImpl(desc);
  }
  if (type == kOfxParamTypeCustom) {
    return new CustomImpl(desc, set);
  }
  if (type == kOfxParamTypeRGBA) {
    return new RGBAImpl(desc, set, 4);
  }
  if (type == kOfxParamTypeRGB) {
    return new RGBImpl(desc, set, 3);
  }
  if (type == kOfxParamTypeDouble2D) {
    return new Double2DImpl(desc, set, 2);
  }
  if (type == kOfxParamTypeInteger2D) {
    return new Integer2DImpl(desc, set, 2);
  }
  if (type == kOfxParamTypeDouble3D) {
    return new Double3DImpl(desc, set, 3);
  }
  if (type == kOfxParamTypeInteger3D) {
    return new Integer3DImpl(desc, set, 3);
  }

  return nullptr;
}

}  // namespace ofxhost
