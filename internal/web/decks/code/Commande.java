import java.util.ArrayList;
import java.util.List;

class Commande {
    protected final List<Ligne> lignes = new ArrayList<>();

    void ajouter(Ligne ligne) { lignes.add(ligne); }
}
