/*
 * OfxHost - an OpenFX host for still images.
 *
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

#ifndef OFXHOST_PARAMS_H
#define OFXHOST_PARAMS_H

/* The seam between the host and its parameter classes.
 *
 * Forward declarations rather than the OpenFX headers: the one caller builds an instance and
 * hands the result back as the same pointer type the library declared, so it needs no
 * knowledge of the shapes behind it. params.cpp includes the real headers.
 */
namespace OFX {
namespace Host {
namespace Param {
class Descriptor;
class Instance;
class SetInstance;
}  // namespace Param
}  // namespace Host
}  // namespace OFX

namespace ofxhost {

/* Creates the host parameter object for one declared parameter.
 *
 * Returns nullptr for a type this host does not implement. The caller reports that, because
 * the message travels through the instance's callbacks and a parameter has no business owning
 * them.
 *
 * The returned object is owned by the parameter set the library holds; the library deletes it
 * when the instance is destroyed.
 */
OFX::Host::Param::Instance *createParam(OFX::Host::Param::Descriptor &desc,
                                        OFX::Host::Param::SetInstance *set);

/* Reports whether an optional flag property, such as enabled or secret, is set on a
 * parameter. Both translation units read these, and a host that guesses instead of asking
 * reports a flag that the plugin did not set. */
bool flagProperty(const OFX::Host::Param::Instance &param, const char *name);

}  // namespace ofxhost

#endif  // OFXHOST_PARAMS_H
