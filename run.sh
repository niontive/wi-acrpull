make undeploy
make
make docker-build docker-push IMG=controllerreg.azurecr.io/wi-acrpull:sixth
make install
make deploy IMG=controllerreg.azurecr.io/wi-acrpull:sixth
kubectl apply -f ./config/samples/wi-acrpull_v1_wipullbinding.yaml 